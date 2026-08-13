package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (a *application) exportExcel(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var cases []Case
	if err := json.NewDecoder(r.Body).Decode(&cases); err != nil {
		http.Error(w, "Некорректные данные экспорта", http.StatusBadRequest)
		return
	}
	filename := "completion-plan.xlsx"
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	book := zip.NewWriter(w)
	files := map[string]string{
		"[Content_Types].xml":        `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`,
		"_rels/.rels":                `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":            `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Отслеживание" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   exportSheet(cases),
	}
	for name, body := range files {
		part, err := book.Create(name)
		if err != nil {
			return
		}
		_, _ = part.Write([]byte(body))
	}
	_ = book.Close()
}

func exportSheet(cases []Case) string {
	headers := []string{"ФИО", "Номер дела", "АУ", "СЗ на начало", "СЗ текущее", "Стадия сделки на начало", "Стадия сделки текущая"}
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	writeExcelRow(&sheet, 1, headers)
	for i, item := range cases {
		writeExcelRow(&sheet, i+2, []string{item.Name, item.CaseNumber, item.Manager, dateRU(item.BaselineHearing), dateRU(item.CurrentHearing), item.BaselineStage, item.CurrentStage})
	}
	sheet.WriteString(`</sheetData><autoFilter ref="A1:G` + fmt.Sprint(len(cases)+1) + `"/></worksheet>`)
	return sheet.String()
}

func writeExcelRow(sheet *strings.Builder, number int, values []string) {
	sheet.WriteString(`<row r="` + fmt.Sprint(number) + `">`)
	for column, value := range values {
		ref := excelColumn(column+1) + fmt.Sprint(number)
		sheet.WriteString(`<c r="` + ref + `" t="inlineStr"><is><t xml:space="preserve">` + xmlText(value) + `</t></is></c>`)
	}
	sheet.WriteString(`</row>`)
}

func excelColumn(number int) string {
	result := ""
	for number > 0 {
		number--
		result = string(rune('A'+number%26)) + result
		number /= 26
	}
	return result
}

func xmlText(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}
