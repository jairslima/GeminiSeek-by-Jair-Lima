package sync

import (
	"testing"
)

func TestFormatBalanceSummary(t *testing.T) {
	if got := FormatBalanceSummary(nil); got != "indisponível" {
		t.Fatalf("expected indisponível for nil balance, got %s", got)
	}

	bal := &BalanceResponse{
		IsAvailable: true,
		BalanceInfos: []BalanceInfo{
			{
				Currency:        "USD",
				TotalBalance:    "4.76",
				ToppedUpBalance: "4.76",
				GrantedBalance:  "0.00",
			},
		},
	}
	got := FormatBalanceSummary(bal)
	expected := "4.76 USD (recarga: 4.76, bônus: 0.00) [ativo]"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}
