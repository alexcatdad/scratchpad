/** Stable ASCII identifiers may contain colons, never URL delimiters or dot segments. */
export function isOpaqueId(value: string): boolean {
  return (
    value.length > 0 &&
    value.length <= 500 &&
    /^[A-Za-z0-9_.:~-]+$/.test(value) &&
    value !== "." &&
    value !== ".."
  );
}
export const encodedId = (value: string): string => encodeURIComponent(value);
export function decodedId(value: string): string {
  const id = decodeURIComponent(value);
  if (!isOpaqueId(id)) throw new Error("Invalid entity identifier.");
  return id;
}
