package main

type Case struct {
	Key             string `json:"key"`
	DealID          string `json:"deal_id"`
	Name            string `json:"name"`
	CaseNumber      string `json:"case_number"`
	Manager         string `json:"manager"`
	BaselineHearing string `json:"baseline_hearing"`
	CurrentHearing  string `json:"current_hearing"`
	BaselineStatus  string `json:"baseline_status"`
	CurrentStatus   string `json:"current_status"`
	BaselineStage   string `json:"baseline_stage"`
	CurrentStage    string `json:"current_stage"`
	CourtRequestOKK string `json:"court_request_okk"`
	ReportOKK       string `json:"report_okk"`
}

func (c Case) IsPostponed() bool {
	return c.BaselineHearing != "" && c.CurrentHearing != "" && c.BaselineHearing != c.CurrentHearing
}
