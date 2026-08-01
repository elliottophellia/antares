export function toolsetsOrEmpty(value: string[] | null | undefined): string[] {
  return Array.isArray(value) ? value : []
}
