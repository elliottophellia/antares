import { useEffect, useState } from 'react'
import { Navigate, Outlet } from 'react-router-dom'
import { get } from '@/lib/api'

interface AuthStatus {
  password_required?: boolean
  authenticated?: boolean
}

/**
 * Holds the dashboard behind the login when a password is configured. When no
 * password is set — or the backend is unreachable — the gate opens rather than
 * trapping the user, matching SetupGate's fail-open stance.
 */
export function LoginGate() {
  const [allowed, setAllowed] = useState<boolean | null>(null)

  useEffect(() => {
    let alive = true
    get<AuthStatus>('/auth/status')
      .then((s) => alive && setAllowed(!s.password_required || !!s.authenticated))
      .catch(() => alive && setAllowed(true))
    return () => {
      alive = false
    }
  }, [])

  if (allowed === null) return null
  if (!allowed) return <Navigate to="/login" replace />
  return <Outlet />
}
