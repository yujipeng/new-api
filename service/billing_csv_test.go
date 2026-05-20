package service

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriteUserBillCSV_HasBomAndHeaders(t *testing.T) {
	var buf bytes.Buffer
	rows := []UserDailyBillRow{
		{StatDate: "2026-05-19", UserId: 7, UserGroup: "g1", ModelName: "gpt-4o",
			RequestCount: 5, PromptTokens: 100, CompletionTokens: 50, RevenueQuota: 200},
	}
	assert.NoError(t, WriteUserBillCSV(&buf, rows))

	body := buf.Bytes()
	if assert.GreaterOrEqual(t, len(body), 3) {
		assert.Equal(t, []byte{0xEF, 0xBB, 0xBF}, body[:3], "must start with UTF-8 BOM")
	}
	text := string(body[3:])
	header, _, _ := strings.Cut(text, "\n")
	assert.Equal(t, "stat_date,user_id,user_group,model_name,request_count,prompt_tokens,completion_tokens,revenue_quota", header)
	assert.Contains(t, text, "2026-05-19,7,g1,gpt-4o,5,100,50,200")
	// Critical AC-7: no cost / profit columns in user export.
	assert.NotContains(t, text, "cost_quota")
	assert.NotContains(t, text, "profit_quota")
}

func TestWriteAdminFullBillCSV_AllColumns(t *testing.T) {
	var buf bytes.Buffer
	rows := []AdminFullBillRow{
		{StatDate: "2026-05-19", UserId: 1, UserGroup: "g", ChannelId: 2, ChannelGroup: "cg",
			ModelName: "m", RequestCount: 1, PromptTokens: 2, CompletionTokens: 3,
			RevenueQuota: 10, CostQuota: 6, ProfitQuota: 4, CostMissing: false},
		{StatDate: "2026-05-19", UserId: 1, ChannelId: 3, ModelName: "n",
			RequestCount: 1, RevenueQuota: 10, CostMissing: true},
	}
	assert.NoError(t, WriteAdminFullBillCSV(&buf, rows))

	body := buf.Bytes()
	assert.Equal(t, []byte{0xEF, 0xBB, 0xBF}, body[:3])
	text := string(body[3:])
	header, _, _ := strings.Cut(text, "\n")
	assert.Equal(t, "stat_date,user_id,user_group,channel_id,channel_group,model_name,request_count,prompt_tokens,completion_tokens,revenue_quota,cost_quota,profit_quota,cost_missing", header)
	// Row 1 row has cost_missing=0
	assert.Contains(t, text, "2026-05-19,1,g,2,cg,m,1,2,3,10,6,4,0")
	// Row 2 has cost_missing=1
	assert.Contains(t, text, ",10,0,0,1\n")
}

func TestCSVFilename_FormatsRange(t *testing.T) {
	assert.Equal(t, "admin_full_bill_2026-05-01_2026-05-31.csv",
		CSVFilename("admin_full", "2026-05-01", "2026-05-31"))
	assert.Equal(t, "channel_bill.csv", CSVFilename("channel", "", ""))
}
