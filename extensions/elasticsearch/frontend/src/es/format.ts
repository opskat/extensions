const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

/** A byte count for display; null (a closed index) is a dash. */
export function formatBytes(bytes: number | null): string {
  if (bytes === null) return "—";
  let n = bytes;
  let unit = 0;
  while (n >= 1024 && unit < UNITS.length - 1) {
    n /= 1024;
    unit++;
  }
  return unit === 0 ? `${n} B` : `${n.toFixed(1)} ${UNITS[unit]}`;
}

/** A compact count (1.2M, 12K) in the page's language; null (a closed index) is a dash. */
export function formatCount(n: number | null, lang: string): string {
  if (n === null) return "—";
  return new Intl.NumberFormat(lang, { notation: "compact", maximumFractionDigits: 1 }).format(n);
}

/** A full count with grouping (12,431). */
export function formatNumber(n: number, lang: string): string {
  return new Intl.NumberFormat(lang).format(n);
}
