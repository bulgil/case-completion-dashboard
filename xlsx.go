package main

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ImportedRow struct {
	Key, DealID, Name, CaseNumber, Manager, Hearing, Status, DealStage string
}

type workbookXML struct {
	Sheets []struct {
		Name         string `xml:"name,attr"`
		Relationship string `xml:"id,attr"`
	} `xml:"sheets>sheet"`
}
type relationshipsXML struct {
	Items []struct {
		ID     string `xml:"Id,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}
type sharedStringsXML struct {
	Items []struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}
type worksheetXML struct {
	Rows []sheetRow `xml:"sheetData>row"`
}
type sheetRow struct {
	Hidden bool        `xml:"hidden,attr"`
	Cells  []sheetCell `xml:"c"`
}
type sheetCell struct {
	Ref    string `xml:"r,attr"`
	Type   string `xml:"t,attr"`
	Value  string `xml:"v"`
	Inline string `xml:"is>t"`
}

func ParseWorkbook(path string) ([]ImportedRow, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("это не корректный XLSX-файл")
	}
	defer z.Close()
	files := map[string]*zip.File{}
	for _, file := range z.File {
		files[filepath.ToSlash(file.Name)] = file
	}
	var workbook workbookXML
	if err := decodeXML(files["xl/workbook.xml"], &workbook); err != nil {
		return nil, fmt.Errorf("структура книги: %w", err)
	}
	if len(workbook.Sheets) == 0 {
		return nil, fmt.Errorf("в книге нет листов")
	}
	var rels relationshipsXML
	if err := decodeXML(files["xl/_rels/workbook.xml.rels"], &rels); err != nil {
		return nil, fmt.Errorf("связи книги: %w", err)
	}
	target := ""
	for _, rel := range rels.Items {
		if rel.ID == workbook.Sheets[0].Relationship {
			target = rel.Target
			break
		}
	}
	if target == "" {
		return nil, fmt.Errorf("не найден первый лист")
	}
	target = strings.TrimPrefix(filepath.ToSlash(target), "/")
	if !strings.HasPrefix(target, "xl/") {
		target = "xl/" + target
	}
	var stringsTable sharedStringsXML
	if file := files["xl/sharedStrings.xml"]; file != nil {
		_ = decodeXML(file, &stringsTable)
	}
	shared := make([]string, len(stringsTable.Items))
	for i, item := range stringsTable.Items {
		shared[i] = item.Text
		for _, run := range item.Runs {
			shared[i] += run.Text
		}
	}
	var sheet worksheetXML
	if err := decodeXML(files[target], &sheet); err != nil {
		return nil, fmt.Errorf("лист: %w", err)
	}
	if len(sheet.Rows) < 2 {
		return nil, fmt.Errorf("таблица пуста")
	}
	values := func(row sheetRow) map[int]string {
		result := map[int]string{}
		for _, cell := range row.Cells {
			col := columnIndex(cell.Ref)
			value := cell.Value
			switch cell.Type {
			case "s":
				if idx, e := strconv.Atoi(value); e == nil && idx >= 0 && idx < len(shared) {
					value = shared[idx]
				}
			case "inlineStr":
				value = cell.Inline
			}
			result[col] = strings.TrimSpace(value)
		}
		return result
	}
	headers := values(sheet.Rows[0])
	byName := map[string]int{}
	for col, header := range headers {
		byName[normalizeHeader(header)] = col
	}
	required := []string{"название", "контакт: номер дела", "контакт: арбитражный управляющий", "контакт: сз завершение", "контакт: статус"}
	for _, name := range required {
		if _, ok := byName[name]; !ok {
			return nil, fmt.Errorf("не найдена колонка «%s»", name)
		}
	}
	rows := make([]ImportedRow, 0, len(sheet.Rows)-1)
	for _, xmlRow := range sheet.Rows[1:] {
		if xmlRow.Hidden {
			continue
		}
		cells := values(xmlRow)
		get := func(name string) string { return cells[byName[name]] }
		contactID := get("контакт: id")
		caseNumber := get("контакт: номер дела")
		name := get("название")
		key := contactID
		if key == "" {
			key = strings.ToLower(caseNumber + "|" + name)
		}
		if key == "" || (caseNumber == "" && name == "") {
			continue
		}
		rows = append(rows, ImportedRow{Key: key, DealID: get("id"), Name: name, CaseNumber: caseNumber,
			Manager: get("контакт: арбитражный управляющий"), Hearing: excelDate(get("контакт: сз завершение")),
			Status: get("контакт: статус"), DealStage: get("стадия сделки")})
	}
	return rows, nil
}

func decodeXML(file *zip.File, target any) error {
	if file == nil {
		return fmt.Errorf("не найден обязательный XML")
	}
	r, err := file.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	decoder := xml.NewDecoder(io.LimitReader(r, 64<<20))
	return decoder.Decode(target)
}

func normalizeHeader(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func columnIndex(ref string) int {
	index := 0
	for _, char := range ref {
		if char < 'A' || char > 'Z' {
			break
		}
		index = index*26 + int(char-'A'+1)
	}
	return index - 1
}

func excelDate(value string) string {
	if value == "" {
		return ""
	}
	if serial, err := strconv.ParseFloat(value, 64); err == nil {
		base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		return base.Add(time.Duration(serial*24) * time.Hour).Format("2006-01-02")
	}
	for _, layout := range []string{"02.01.2006", "2006-01-02", "1/2/06", "1/2/2006"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date.Format("2006-01-02")
		}
	}
	return value
}
