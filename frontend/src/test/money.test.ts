import { describe, expect, it } from 'vitest';
import { formatCents } from '../lib/money';

describe('formatCents', () => {
  it.each([
    [0, '$0.00'],
    [5, '$0.05'],
    [412, '$4.12'],
    [15000, '$150.00'],
    [123456789, '$1,234,567.89'],
    [-9000, '−$90.00'],
  ])('formats %i cents as %s', (cents, text) => {
    expect(formatCents(cents)).toBe(text);
  });

  it('adds a plus sign when asked', () => {
    expect(formatCents(111, { signed: true })).toBe('+$1.11');
    expect(formatCents(0, { signed: true })).toBe('$0.00');
  });

  it('refuses non-integer amounts', () => {
    expect(() => formatCents(1.5)).toThrow(RangeError);
    expect(() => formatCents(Number.NaN)).toThrow(RangeError);
  });
});
