/**
 * Money is integer cents, USD (CLAUDE.md rule 5). Formatting works on the integer directly, so no
 * float ever touches an amount.
 */
export function formatCents(cents: number, opts: { signed?: boolean } = {}): string {
  if (!Number.isSafeInteger(cents)) throw new RangeError(`formatCents expects integer cents, got ${cents}`);
  const negative = cents < 0;
  const abs = Math.abs(cents);
  const dollars = Math.trunc(abs / 100);
  const rem = abs % 100;
  const body = `$${dollars.toLocaleString('en-US')}.${rem.toString().padStart(2, '0')}`;
  if (negative) return `−${body}`;
  return opts.signed && cents > 0 ? `+${body}` : body;
}
