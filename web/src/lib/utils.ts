import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

/** Merge conditional class names, resolving Tailwind conflicts. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/** Format a byte count for display. */
export function formatBytes(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

/** Compact number formatting: 1.2k, 3.4M. */
export function formatCount(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1).replace(/\.0$/, '')}k`
  return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`
}

/** Relative time in Indonesian, falling back to a date for old items. */
export function timeAgo(iso: string | Date | null | undefined): string {
  if (!iso) return '—'
  const then = typeof iso === 'string' ? new Date(iso) : iso
  const secs = Math.floor((Date.now() - then.getTime()) / 1000)
  if (Number.isNaN(secs)) return '—'
  if (secs < 45) return 'baru saja'
  if (secs < 3600) return `${Math.floor(secs / 60)} menit lalu`
  if (secs < 86400) return `${Math.floor(secs / 3600)} jam lalu`
  if (secs < 604800) return `${Math.floor(secs / 86400)} hari lalu`
  return then.toLocaleDateString('id-ID', { day: 'numeric', month: 'short', year: 'numeric' })
}

/** Stable short id for optimistic UI rows. */
export function shortId(prefix = 'id'): string {
  return `${prefix}_${Math.random().toString(36).slice(2, 10)}`
}
