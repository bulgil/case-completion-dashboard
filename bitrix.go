package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	statusField  = "UF_CRM_1708427400582"
	hearingField = "UF_CRM_1708427240655"
)

type BitrixTracker struct {
	store   *Store
	webhook string
	client  *http.Client
	mu      sync.Mutex
}

func NewBitrixTracker(store *Store, webhook string) *BitrixTracker {
	return &BitrixTracker{store: store, webhook: strings.TrimRight(strings.TrimSpace(webhook), "/") + "/", client: &http.Client{Timeout: 45 * time.Second}}
}

func (b *BitrixTracker) Enabled() bool { return strings.TrimSpace(b.webhook) != "/" }

func (b *BitrixTracker) Run(ctx context.Context) {
	if !b.Enabled() {
		return
	}
	// Let the HTTP server start before the first refresh.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	_, _ = b.Sync(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = b.Sync(ctx)
		}
	}
}

func (b *BitrixTracker) Sync(ctx context.Context) (int, error) {
	if !b.Enabled() {
		return 0, fmt.Errorf("BITRIX_WEBHOOK_URL не настроен")
	}
	if !b.mu.TryLock() {
		return 0, fmt.Errorf("обновление уже выполняется")
	}
	defer b.mu.Unlock()
	cases := b.store.Snapshot().Cases
	if len(cases) == 0 {
		return 0, nil
	}

	var fieldsPayload map[string]any
	if err := b.call(ctx, "crm.contact.fields", url.Values{}, &fieldsPayload); err != nil {
		return 0, err
	}
	fields := object(fieldsPayload["result"])
	statusMeta := object(fields[statusField])
	if len(statusMeta) == 0 {
		return 0, fmt.Errorf("Bitrix24 не вернул поле контакта %s", statusField)
	}

	updates := make([]CurrentUpdate, 0, len(cases))
	stageIDs := map[string]struct{}{}
	for offset := 0; offset < len(cases); offset += 25 {
		end := offset + 25
		if end > len(cases) {
			end = len(cases)
		}
		commands := map[string]string{}
		for _, item := range cases[offset:end] {
			commands["c_"+item.Key] = "crm.contact.get?id=" + url.QueryEscape(item.Key)
			if item.DealID != "" {
				commands["d_"+item.Key] = "crm.deal.get?id=" + url.QueryEscape(item.DealID)
			}
		}
		results, err := b.batch(ctx, commands)
		if err != nil {
			return len(updates), err
		}
		for _, saved := range cases[offset:end] {
			contact := object(results["c_"+saved.Key])
			if len(contact) == 0 {
				continue
			}
			update := CurrentUpdate{Key: saved.Key, Status: enumValue(statusMeta, contact[statusField]), Hearing: normalizeBitrixDate(contact[hearingField]), Stage: saved.CurrentStage}
			deal := object(results["d_"+saved.Key])
			if id := firstString(deal, "STAGE_ID", "stageId"); id != "" {
				update.Stage = id
				stageIDs[id] = struct{}{}
			}
			updates = append(updates, update)
		}
	}
	if len(updates) == 0 {
		return 0, fmt.Errorf("Bitrix24 не вернул ни одного отслеживаемого контакта; проверьте Contact ID и права webhook на CRM")
	}
	stageNames := b.stageNames(ctx, stageIDs)
	for i := range updates {
		if name := stageNames[updates[i].Stage]; name != "" {
			updates[i].Stage = name
		}
	}
	return b.store.UpdateCurrent(updates)
}

func (b *BitrixTracker) stageNames(ctx context.Context, ids map[string]struct{}) map[string]string {
	result := map[string]string{}
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for offset := 0; offset < len(keys); offset += 50 {
		end := offset + 50
		if end > len(keys) {
			end = len(keys)
		}
		commands := map[string]string{}
		for i, id := range keys[offset:end] {
			commands[fmt.Sprintf("s_%d", i)] = "crm.status.list?filter%5BSTATUS_ID%5D=" + url.QueryEscape(id)
		}
		payload, err := b.batch(ctx, commands)
		if err != nil {
			continue
		}
		for i, id := range keys[offset:end] {
			items := array(payload[fmt.Sprintf("s_%d", i)])
			if len(items) == 0 {
				continue
			}
			item := object(items[0])
			result[id] = stringValue(item["NAME"])
		}
	}
	return result
}

func (b *BitrixTracker) batch(ctx context.Context, commands map[string]string) (map[string]any, error) {
	form := url.Values{"halt": {"0"}}
	for key, command := range commands {
		form.Set("cmd["+key+"]", command)
	}
	var payload map[string]any
	if err := b.call(ctx, "batch", form, &payload); err != nil {
		return nil, err
	}
	return object(object(payload["result"])["result"]), nil
}

func (b *BitrixTracker) call(ctx context.Context, method string, form url.Values, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.webhook+method+".json", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("Bitrix24 %s: %w", method, err)
	}
	defer resp.Body.Close()
	var raw map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("Bitrix24 %s: %w", method, err)
	}
	if code := stringValue(raw["error"]); code != "" {
		return fmt.Errorf("Bitrix24 %s: %s: %s", method, code, stringValue(raw["error_description"]))
	}
	bytes, _ := json.Marshal(raw)
	return json.Unmarshal(bytes, target)
}

func object(value any) map[string]any {
	if v, ok := value.(map[string]any); ok {
		return v
	}
	return map[string]any{}
}
func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if values, ok := value.([]any); ok && len(values) > 0 {
		value = values[0]
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(item[key]); value != "" {
			return value
		}
	}
	return ""
}
func enumValue(meta map[string]any, raw any) string {
	value := stringValue(raw)
	for _, option := range array(meta["items"]) {
		item := object(option)
		if stringValue(item["ID"]) == value || stringValue(item["id"]) == value {
			if label := stringValue(item["VALUE"]); label != "" {
				return label
			}
			return stringValue(item["value"])
		}
	}
	return value
}
func array(value any) []any {
	if v, ok := value.([]any); ok {
		return v
	}
	return nil
}
func normalizeBitrixDate(value any) string {
	raw := stringValue(value)
	if len(raw) >= 10 && raw[4] == '-' && raw[7] == '-' {
		return raw[:10]
	}
	return raw
}
