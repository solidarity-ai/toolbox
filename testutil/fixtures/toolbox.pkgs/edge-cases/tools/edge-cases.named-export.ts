/**
 * @accessMode readOnly
 * @idempotent
 */
export default function computeHash(data: string, algorithm: "sha256" | "md5" | "sha512"): string {
  return data;
}
