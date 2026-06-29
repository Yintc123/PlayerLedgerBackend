package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCanTransition_AllPairs 以矩陣窮舉所有 (from,to) 組合，
// 對照 deposit-records-model.md §7.5 的狀態機唯一真相。
func TestCanTransition_AllPairs(t *testing.T) {
	all := []DepositStatus{
		DepositStatusPending,
		DepositStatusCompleted,
		DepositStatusFailed,
		DepositStatusCancelled,
		DepositStatusRefunded,
	}

	// allowed 僅列出合法轉移；其餘一律視為非法。
	allowed := map[DepositStatus]map[DepositStatus]bool{
		DepositStatusPending:   {DepositStatusCompleted: true, DepositStatusFailed: true, DepositStatusCancelled: true},
		DepositStatusCompleted: {DepositStatusRefunded: true},
	}

	for _, from := range all {
		for _, to := range all {
			want := allowed[from][to]
			got := CanTransition(from, to)
			assert.Equalf(t, want, got, "CanTransition(%q, %q)", from, to)
		}
	}
}

func TestCanTransition_PendingToCompleted_Allowed(t *testing.T) {
	assert.True(t, CanTransition(DepositStatusPending, DepositStatusCompleted))
}

func TestCanTransition_CompletedToRefunded_Allowed(t *testing.T) {
	assert.True(t, CanTransition(DepositStatusCompleted, DepositStatusRefunded))
}

func TestCanTransition_PendingToRefunded_Rejected(t *testing.T) {
	// pending 不可直接退款，必須先 completed。
	assert.False(t, CanTransition(DepositStatusPending, DepositStatusRefunded))
}

func TestCanTransition_SameStatus_Rejected(t *testing.T) {
	assert.False(t, CanTransition(DepositStatusPending, DepositStatusPending))
}

func TestCanTransition_TerminalStatuses_NoOutgoing(t *testing.T) {
	terminal := []DepositStatus{DepositStatusFailed, DepositStatusCancelled, DepositStatusRefunded}
	targets := []DepositStatus{
		DepositStatusPending, DepositStatusCompleted, DepositStatusFailed,
		DepositStatusCancelled, DepositStatusRefunded,
	}
	for _, from := range terminal {
		for _, to := range targets {
			assert.Falsef(t, CanTransition(from, to), "terminal %q should not transition to %q", from, to)
		}
	}
}

func TestCanTransition_UnknownStatus_Rejected(t *testing.T) {
	assert.False(t, CanTransition(DepositStatus("bogus"), DepositStatusCompleted))
	assert.False(t, CanTransition(DepositStatusPending, DepositStatus("bogus")))
}
