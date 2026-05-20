package service

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
)

// CSV scopes — kept in one place so the controller and the writer agree on
// header order and on which struct each scope yields.
const (
	CSVScopeUser        = "user"
	CSVScopeAdminUser   = "admin_user"
	CSVScopeChannel     = "channel"
	CSVScopeAdminFull   = "admin_full"
)

// utf8BOM is the optional byte-order mark Excel expects when opening UTF-8 CSV.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func WriteUserBillCSV(w io.Writer, rows []UserDailyBillRow) error {
	if _, err := w.Write(utf8BOM); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{
		"stat_date", "user_id", "user_group", "model_name",
		"request_count", "prompt_tokens", "completion_tokens", "revenue_quota",
	}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write([]string{
			r.StatDate,
			strconv.Itoa(r.UserId),
			r.UserGroup,
			r.ModelName,
			strconv.FormatInt(r.RequestCount, 10),
			strconv.FormatInt(r.PromptTokens, 10),
			strconv.FormatInt(r.CompletionTokens, 10),
			strconv.FormatInt(r.RevenueQuota, 10),
		}); err != nil {
			return err
		}
	}
	return nil
}

func WriteAdminUserBillCSV(w io.Writer, rows []AdminUserBillRow) error {
	if _, err := w.Write(utf8BOM); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{
		"stat_date", "user_id", "user_group", "model_name",
		"request_count", "prompt_tokens", "completion_tokens",
		"revenue_quota", "cost_quota", "profit_quota", "cost_missing",
	}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write([]string{
			r.StatDate,
			strconv.Itoa(r.UserId),
			r.UserGroup,
			r.ModelName,
			strconv.FormatInt(r.RequestCount, 10),
			strconv.FormatInt(r.PromptTokens, 10),
			strconv.FormatInt(r.CompletionTokens, 10),
			strconv.FormatInt(r.RevenueQuota, 10),
			strconv.FormatInt(r.CostQuota, 10),
			strconv.FormatInt(r.ProfitQuota, 10),
			strconv.Itoa(r.CostMissing),
		}); err != nil {
			return err
		}
	}
	return nil
}

func WriteChannelBillCSV(w io.Writer, rows []ChannelBillRow) error {
	if _, err := w.Write(utf8BOM); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{
		"stat_date", "channel_id", "channel_group", "model_name",
		"request_count", "revenue_quota", "cost_quota", "profit_quota", "cost_missing",
	}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write([]string{
			r.StatDate,
			strconv.Itoa(r.ChannelId),
			r.ChannelGroup,
			r.ModelName,
			strconv.FormatInt(r.RequestCount, 10),
			strconv.FormatInt(r.RevenueQuota, 10),
			strconv.FormatInt(r.CostQuota, 10),
			strconv.FormatInt(r.ProfitQuota, 10),
			strconv.Itoa(r.CostMissing),
		}); err != nil {
			return err
		}
	}
	return nil
}

func WriteAdminFullBillCSV(w io.Writer, rows []AdminFullBillRow) error {
	if _, err := w.Write(utf8BOM); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{
		"stat_date", "user_id", "user_group", "channel_id", "channel_group", "model_name",
		"request_count", "prompt_tokens", "completion_tokens",
		"revenue_quota", "cost_quota", "profit_quota", "cost_missing",
	}); err != nil {
		return err
	}
	for _, r := range rows {
		costMissing := "0"
		if r.CostMissing {
			costMissing = "1"
		}
		if err := cw.Write([]string{
			r.StatDate,
			strconv.Itoa(r.UserId),
			r.UserGroup,
			strconv.Itoa(r.ChannelId),
			r.ChannelGroup,
			r.ModelName,
			strconv.FormatInt(r.RequestCount, 10),
			strconv.FormatInt(r.PromptTokens, 10),
			strconv.FormatInt(r.CompletionTokens, 10),
			strconv.FormatInt(r.RevenueQuota, 10),
			strconv.FormatInt(r.CostQuota, 10),
			strconv.FormatInt(r.ProfitQuota, 10),
			costMissing,
		}); err != nil {
			return err
		}
	}
	return nil
}

// CSVFilename derives the Content-Disposition filename for the scope+date range.
func CSVFilename(scope, start, end string) string {
	if start == "" && end == "" {
		return fmt.Sprintf("%s_bill.csv", scope)
	}
	return fmt.Sprintf("%s_bill_%s_%s.csv", scope, start, end)
}
