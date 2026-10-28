/** Human-readable message from a thrown value (server message when available). */
export function errMessage(e: unknown): string {
  return e instanceof Error && e.message ? e.message : String(e)
}
