/**
 * Compute the hash of input data.
 * @accessMode readOnly
 * @idempotent
 * @param data - The data to hash
 * @param algorithm - Hash algorithm to use
 */
export default function computeHash(data: string, algorithm: "sha256" | "md5" | "sha512"): string {
  return data;
}
