package money

import (
	"math/big"
	"testing"
)

func dec(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, err := Decimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPayoutCents(t *testing.T) {
	cases := []struct {
		cover  int64
		share  string
		factor *big.Rat
		want   int64
	}{
		{100, "0.6", FactorHalf, 30},     // the seeded test cover under R2
		{100, "0.6", FactorFull, 60},     // test cover under R1/R3
		{15000, "0.6", FactorFull, 9000}, // $150 cover, 4h tier
		{15000, "0.6", FactorHalf, 4500},
		{5000, "0.6", FactorHalf, 1500},
		{30000, "1.0", FactorFull, 30000},
		{101, "0.6", FactorHalf, 30}, // 30.3 rounds down
		{99, "0.6", FactorFull, 59},  // 59.4 rounds down
		{100, "6e-1", FactorFull, 60},
	}
	for _, c := range cases {
		got, err := PayoutCents(c.cover, dec(t, c.share), c.factor)
		if err != nil || got != c.want {
			t.Errorf("PayoutCents(%d, %s, %v) = %d, %v; want %d", c.cover, c.share, c.factor, got, err, c.want)
		}
	}
	if _, err := PayoutCents(-1, dec(t, "0.6"), FactorFull); err == nil {
		t.Error("negative cover: want error")
	}
	if _, err := Decimal("abc"); err == nil {
		t.Error("bad decimal: want error")
	}
}

func TestTopUpCents(t *testing.T) {
	got, err := TopUpCents(15000, dec(t, "0.6"), dec(t, "1.0"), FactorFull)
	if err != nil || got != 6000 {
		t.Fatalf("got %d %v", got, err)
	}
	got, err = TopUpCents(100, dec(t, "0.6"), dec(t, "1.0"), FactorHalf)
	if err != nil || got != 20 {
		t.Fatalf("got %d %v", got, err)
	}
}

func TestComputePremium(t *testing.T) {
	// The CONTRACTS.md section 4 example inputs (illustrative only).
	in := PremiumInputs{
		CoverCents:            15000,
		ExpectedEventsPerYear: dec(t, "0.61"),
		Prob24hGivenEvent:     dec(t, "0.18"),
		ProbPremisesAffected:  dec(t, "0.22"),
		RiskMarginRate:        dec(t, "0.35"),
		ExpenseLoadCents:      dec(t, "150"),
		T4:                    dec(t, "0.6"),
		T24:                   dec(t, "1.0"),
	}
	p, err := ComputePremium(in)
	if err != nil {
		t.Fatal(err)
	}
	// share = 0.6 + 0.4*0.18 = 0.672; expected = 0.61*0.672*0.22*15000 = 1352.736
	// premium = 1352.736/12*1.35 + 150 = 302.1828 -> 302
	if p.ExpectedAnnualPayoutCents != 1353 || p.PremiumCents != 302 {
		t.Fatalf("got %+v", p)
	}
	want := dec(t, "1352.736")
	if p.ExpectedAnnualPayout.Cmp(want) != 0 {
		t.Errorf("exact expected %v", p.ExpectedAnnualPayout.FloatString(6))
	}
	in.ExpenseLoadCents = nil
	if _, err := ComputePremium(in); err == nil {
		t.Error("nil input: want error")
	}
}

func TestRoundHalfUp(t *testing.T) {
	for in, want := range map[string]int64{"2.5": 3, "2.4999": 2, "0": 0, "0.5": 1, "301.5": 302} {
		got, err := RoundHalfUp(dec(t, in))
		if err != nil || got != want {
			t.Errorf("%s: %d %v", in, got, err)
		}
	}
}
