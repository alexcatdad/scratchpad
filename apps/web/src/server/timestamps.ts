/** Compare ISO instants without rounding user-supplied fractional seconds. */
export function compareTimestamps(left: string, right: string): number {
  const parts = (value: string) => {
    const match = /^(.*?)(?:\.(\d+))?Z$/.exec(value);
    return {
      seconds: Date.parse(match ? `${match[1]}Z` : value),
      fraction: (match?.[2] ?? "").replace(/0+$/, ""),
    };
  };
  const a = parts(left);
  const b = parts(right);
  const seconds = a.seconds - b.seconds;
  if (seconds !== 0) return seconds;
  const length = Math.max(a.fraction.length, b.fraction.length);
  const x = a.fraction.padEnd(length, "0");
  const y = b.fraction.padEnd(length, "0");
  return x < y ? -1 : x > y ? 1 : 0;
}
