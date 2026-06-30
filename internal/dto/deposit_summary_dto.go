package dto

import (
	"time"

	"github.com/yintengching/playerledger/internal/service"
)

// CurrencyTotalsDTO 單一幣別彙總（players-deposit-summary-api.md §5）。
type CurrencyTotalsDTO struct {
	Currency        string  `json:"currency"`
	CompletedCount  int64   `json:"completed_count"`
	CompletedAmount int64   `json:"completed_amount"`
	RefundedCount   int64   `json:"refunded_count"`
	RefundedAmount  int64   `json:"refunded_amount"`
	FailedCount     int64   `json:"failed_count"`
	RefundRate      float64 `json:"refund_rate"`
}

// PlayerDepositSummaryDTO 玩家儲值彙總回應 data。
// 欄位顯式輸出（含 null），不用 omitempty；時間欄位為 RFC3339 UTC 或 null。
type PlayerDepositSummaryDTO struct {
	PlayerID         string              `json:"player_id"`
	TotalsByCurrency []CurrencyTotalsDTO `json:"totals_by_currency"`
	FirstTopupAt     *string             `json:"first_topup_at"`
	LastTopupAt      *string             `json:"last_topup_at"`
	LifetimeDays     *int                `json:"lifetime_days"`
}

// FromDepositSummary 將 service 輸出轉為回應 DTO（snake_case、時間轉 UTC、保證 totals 非 nil slice）。
func FromDepositSummary(out *service.DepositSummaryOutput) PlayerDepositSummaryDTO {
	totals := make([]CurrencyTotalsDTO, 0, len(out.Totals))
	for _, t := range out.Totals {
		totals = append(totals, CurrencyTotalsDTO{
			Currency:        t.Currency,
			CompletedCount:  t.CompletedCount,
			CompletedAmount: t.CompletedAmount,
			RefundedCount:   t.RefundedCount,
			RefundedAmount:  t.RefundedAmount,
			FailedCount:     t.FailedCount,
			RefundRate:      t.RefundRate,
		})
	}
	return PlayerDepositSummaryDTO{
		PlayerID:         out.PlayerID.String(),
		TotalsByCurrency: totals,
		FirstTopupAt:     formatRFC3339UTCPtr(out.FirstTopupAt),
		LastTopupAt:      formatRFC3339UTCPtr(out.LastTopupAt),
		LifetimeDays:     out.LifetimeDays,
	}
}

// formatRFC3339UTCPtr 將 *time.Time 轉為 *RFC3339(UTC) 字串；nil → nil（序列化為 null）。
func formatRFC3339UTCPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
