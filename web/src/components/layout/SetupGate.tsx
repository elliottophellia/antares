import { useEffect, useState } from 'react'
import { Navigate, Outlet } from 'react-router-dom'
import { get } from '@/lib/api'

interface Status {
  needs_setup?: boolean
}

/**
 * Sends a fresh install to onboarding before it can reach a dashboard that
 * cannot do anything yet. A backend that is down is not "unconfigured", so the
 * gate opens rather than trapping the user on the wizard.
 */
export function SetupGate() {
  const [needsSetup, setNeedsSetup] = useState<boolean | null>(null)

  useEffect(() => {
    let alive = true
    get<Status>('/status')
      .then((s) => alive && setNeedsSetup(!!s.needs_setup))
      .catch(() => alive && setNeedsSetup(false))
    return () => {
      alive = false
    }
  }, [])

  if (needsSetup === null) return null
  if (needsSetup) return <Navigate to="/setup" replace />
  return <Outlet />
}
