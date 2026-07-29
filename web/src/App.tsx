import { Suspense, lazy } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/layout/AppShell'
import { SetupGate } from '@/components/layout/SetupGate'
import { LoginGate } from '@/components/layout/LoginGate'
import { ROUTES } from '@/lib/routes'
import { I18nProvider } from '@/lib/i18n'

// Onboarding renders standalone: there is no sidebar to wander off into before
// the agent can answer anything.
const SetupPage = lazy(() => import('@/pages/SetupPage'))
// The dashboard login renders standalone too, before any chrome loads.
const LoginPage = lazy(() => import('@/pages/LoginPage'))

// Dev-only UI prototypes live under src/sample/ (gitignored, never shipped).
// The glob resolves to nothing on a clean checkout, so /sample simply 404s to
// "/" there; when the folder exists, each sample/<name>.tsx mounts at
// /sample/<name>. import.meta.glob is compile-time, so a missing folder is not
// an error — this file stays committable with no hard dependency on it.
const sampleModules = import.meta.glob('./sample/*.tsx')
const sampleRoutes = Object.entries(sampleModules).map(([path, loader]) => {
  const name = path.replace('./sample/', '').replace('.tsx', '')
  const Comp = lazy(loader as () => Promise<{ default: React.ComponentType }>)
  return { name, Comp }
})

export function App() {
  return (
    <I18nProvider>
      <BrowserRouter>
        <Routes>
          <Route
            path="/setup"
            element={
              <Suspense fallback={null}>
                <SetupPage />
              </Suspense>
            }
          />
          <Route
            path="/login"
            element={
              <Suspense fallback={null}>
                <LoginPage />
              </Suspense>
            }
          />
          {/* Dev-only prototype routes, standalone (no chrome, no gates). */}
          {sampleRoutes.map(({ name, Comp }) => (
            <Route
              key={name}
              path={`/sample/${name}`}
              element={
                <Suspense fallback={null}>
                  <Comp />
                </Suspense>
              }
            />
          ))}

          {/* LoginGate holds everything behind the dashboard password (when
              set); SetupGate redirects a fresh install to onboarding; AppShell
              owns the page container, header, and Suspense boundary, so every
              route inside renders with identical chrome. */}
          <Route element={<LoginGate />}>
            <Route element={<SetupGate />}>
              <Route element={<AppShell />}>
                {ROUTES.flatMap(({ path, aliases, component: Component }) =>
                  [path, ...(aliases ?? [])].map((p) => (
                    <Route key={p} path={p} element={<Component />} />
                  )),
                )}
                <Route path="*" element={<Navigate to="/" replace />} />
              </Route>
            </Route>
          </Route>
        </Routes>
      </BrowserRouter>
    </I18nProvider>
  )
}
