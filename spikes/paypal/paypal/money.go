package paypal

import (
	"errors"
	"fmt"
)

// CurrencyUSD is the only currency FourHour uses (CONTRACTS.md section 1).
const CurrencyUSD = "USD"

// ErrNegativeAmount is returned by FormatCents for amounts below zero.
var ErrNegativeAmount = errors.New("paypal: amount in cents must not be negative")

// FormatCents turns integer cents into PayPal's two-decimal string, e.g.
// 15000 -> "150.00", 100 -> "1.00", 5 -> "0.05". Integer arithmetic only.
func FormatCents(cents int64) (string, error) {
	if cents < 0 {
		return "", fmt.Errorf("%w: %d", ErrNegativeAmount, cents)
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100), nil
}

// USD returns an Orders/Payments Money value for the given cents.
func USD(cents int64) (Money, error) {
	v, err := FormatCents(cents)
	if err != nil {
		return Money{}, err
	}
	return Money{CurrencyCode: CurrencyUSD, Value: v}, nil
}

// PayoutUSD returns a Payouts PayoutCurrency value for the given cents.
func PayoutUSD(cents int64) (PayoutCurrency, error) {
	v, err := FormatCents(cents)
	if err != nil {
		return PayoutCurrency{}, err
	}
	return PayoutCurrency{Currency: CurrencyUSD, Value: v}, nil
}
