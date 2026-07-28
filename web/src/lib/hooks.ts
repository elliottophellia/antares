import { useCallback, useEffect, useRef, useState } from 'react'
import { get } from './api'

export interface AsyncState<T> {
  data: T | undefined
  loading: boolean
  error: Error | undefined
  reload: () => void
  setData: (v: T) => void
}

/**
 * Fetch a JSON endpoint with loading/error state. `deps` re-triggers the fetch.
 * Keeps previous data while reloading so lists do not flash empty.
 */
export function useApi<T>(path: string | null, deps: unknown[] = []): AsyncState<T> {
  const [data, setData] = useState<T>()
  const [loading, setLoading] = useState(path !== null)
  const [error, setError] = useState<Error>()
  const [nonce, setNonce] = useState(0)
  const alive = useRef(true)

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  useEffect(() => {
    if (path === null) {
      setLoading(false)
      return
    }
    setLoading(true)
    get<T>(path)
      .then((v) => {
        if (alive.current) {
          setData(v)
          setError(undefined)
        }
      })
      .catch((e: Error) => {
        if (alive.current) setError(e)
      })
      .finally(() => {
        if (alive.current) setLoading(false)
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, nonce, ...deps])

  const reload = useCallback(() => setNonce((n) => n + 1), [])
  return { data, loading, error, reload, setData }
}

/** Poll an endpoint on an interval; pauses while the tab is hidden. A null path
 *  disables fetching (and polling) until it becomes non-null. */
export function usePoll<T>(path: string | null, intervalMs = 5000): AsyncState<T> {
  const state = useApi<T>(path)
  const reload = state.reload

  useEffect(() => {
    let timer: ReturnType<typeof setInterval> | undefined
    const start = () => {
      stop()
      timer = setInterval(() => {
        if (document.visibilityState === 'visible') reload()
      }, intervalMs)
    }
    const stop = () => {
      if (timer) clearInterval(timer)
      timer = undefined
    }
    start()
    document.addEventListener('visibilitychange', start)
    return () => {
      stop()
      document.removeEventListener('visibilitychange', start)
    }
  }, [reload, intervalMs])

  return state
}

/** Persisted state backed by localStorage. */
export function useLocalStorage<T>(key: string, initial: T): [T, (v: T) => void] {
  const [value, setValue] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(key)
      return raw ? (JSON.parse(raw) as T) : initial
    } catch {
      return initial
    }
  })
  const update = useCallback(
    (v: T) => {
      setValue(v)
      try {
        localStorage.setItem(key, JSON.stringify(v))
      } catch {
        /* quota or private mode */
      }
    },
    [key],
  )
  return [value, update]
}

/** True when the viewport is at least `query` wide. */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() =>
    typeof window === 'undefined' ? false : window.matchMedia(query).matches,
  )
  useEffect(() => {
    const mq = window.matchMedia(query)
    const handler = (e: MediaQueryListEvent) => setMatches(e.matches)
    setMatches(mq.matches)
    mq.addEventListener('change', handler)
    return () => mq.removeEventListener('change', handler)
  }, [query])
  return matches
}

/** Scroll an element to the bottom when `dep` changes, unless the user scrolled up. */
export function useStickyScroll(dep: unknown) {
  const ref = useRef<HTMLDivElement>(null)
  const stuck = useRef(true)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const onScroll = () => {
      stuck.current = el.scrollHeight - el.scrollTop - el.clientHeight < 120
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [])

  useEffect(() => {
    const el = ref.current
    if (el && stuck.current) el.scrollTop = el.scrollHeight
  }, [dep])

  return ref
}
