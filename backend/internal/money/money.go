// Package money computes payout and premium amounts in exact arithmetic.
// Amounts are int64 cents; model inputs arrive as JSON decimal strings and
// are read into big.Rat, never float64 (CLAUDE.md rule 5).
package money

import (
	"errors"
	"fmt"
	"math/big"
)

// Decimal parses a JSON number literal (for example "0.6", "1", "6e-1")
// exactly.
func Decimal(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("money: %q is not a decimal number", s)
	}
	return r, nil
}

// Rule factors: R2 pays 50% of schedule, R1 and R3 pay it in full.
var (
	FactorFull = big.NewRat(1, 1)
	FactorHalf = big.NewRat(1, 2)
)

// ErrNegative is returned for negative inputs.
var ErrNegative = errors.New("money: negative input")

// PayoutCents is cover × share × factor, rounded down to a whole cent
// (CONTRACTS.md section 3).
func PayoutCents(coverCents int64, share, factor *big.Rat) (int64, error) {
	if coverCents < 0 || share.Sign() < 0 || factor.Sign() < 0 {
		return 0, ErrNegative
	}
	v := new(big.Rat).SetInt64(coverCents)
	v.Mul(v, share).Mul(v, factor)
	q := new(big.Int).Quo(v.Num(), v.Denom()) // truncation = floor for v >= 0
	if !q.IsInt64() {
		return 0, fmt.Errorf("money: payout overflows int64")
	}
	return q.Int64(), nil
}

// TopUpCents is the 24-hour tier payment: the t24 amount minus what the t4
// tier already paid, never below zero (SPEC.md section 4).
func TopUpCents(coverCents int64, t4Share, t24Share, factor *big.Rat) (int64, error) {
	full, err := PayoutCents(coverCents, t24Share, factor)
	if err != nil {
		return 0, err
	}
	first, err := PayoutCents(coverCents, t4Share, factor)
	if err != nil {
		return 0, err
	}
	if full < first {
		return 0, nil
	}
	return full - first, nil
}

// PremiumInputs are the pricing_table values for one county and cover.
type PremiumInputs struct {
	CoverCents            int64
	ExpectedEventsPerYear *big.Rat
	Prob24hGivenEvent     *big.Rat
	ProbPremisesAffected  *big.Rat
	RiskMarginRate        *big.Rat
	ExpenseLoadCents      *big.Rat
	T4, T24               *big.Rat
}

// Premium is the result of the CONTRACTS.md section 4 formula.
type Premium struct {
	// ExpectedAnnualPayout is exact; ExpectedAnnualPayoutCents is it rounded
	// half up for display only.
	ExpectedAnnualPayout      *big.Rat
	ExpectedAnnualPayoutCents int64
	PremiumCents              int64
}

// ComputePremium: share = t4 + (t24 − t4) × p24; expected annual payout =
// events × share × p_affected × cover; premium = expected / 12 × (1 + risk
// margin) + expense load, rounded half up to a whole cent at the end only.
func ComputePremium(in PremiumInputs) (Premium, error) {
	for _, r := range []*big.Rat{in.ExpectedEventsPerYear, in.Prob24hGivenEvent, in.ProbPremisesAffected, in.RiskMarginRate, in.ExpenseLoadCents, in.T4, in.T24} {
		if r == nil {
			return Premium{}, errors.New("money: premium input is nil")
		}
		if r.Sign() < 0 {
			return Premium{}, ErrNegative
		}
	}
	if in.CoverCents <= 0 {
		return Premium{}, ErrNegative
	}
	share := new(big.Rat).Sub(in.T24, in.T4)
	share.Mul(share, in.Prob24hGivenEvent).Add(share, in.T4)
	exp := new(big.Rat).Mul(in.ExpectedEventsPerYear, share)
	exp.Mul(exp, in.ProbPremisesAffected).Mul(exp, new(big.Rat).SetInt64(in.CoverCents))
	prem := new(big.Rat).Quo(exp, big.NewRat(12, 1))
	prem.Mul(prem, new(big.Rat).Add(big.NewRat(1, 1), in.RiskMarginRate)).Add(prem, in.ExpenseLoadCents)
	p, err := RoundHalfUp(prem)
	if err != nil {
		return Premium{}, err
	}
	e, err := RoundHalfUp(exp)
	if err != nil {
		return Premium{}, err
	}
	return Premium{ExpectedAnnualPayout: exp, ExpectedAnnualPayoutCents: e, PremiumCents: p}, nil
}

// RoundHalfUp rounds a non-negative rational to the nearest integer, halves up.
func RoundHalfUp(r *big.Rat) (int64, error) {
	if r.Sign() < 0 {
		return 0, ErrNegative
	}
	v := new(big.Rat).Add(r, big.NewRat(1, 2))
	q := new(big.Int).Quo(v.Num(), v.Denom())
	if !q.IsInt64() {
		return 0, fmt.Errorf("money: value overflows int64")
	}
	return q.Int64(), nil
}
