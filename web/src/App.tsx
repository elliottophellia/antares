import { Suspense, lazy } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/layout/AppShell'
import { SetupGate } from '@/components/layout/SetupGate'
import { ROUTES } from '@/lib/routes'
import { I18nProvider } from '@/lib/i18n'

// Onboarding renders standalone: there is no sidebar to wander off into before
// the agent can answer anything.
const SetupPage = lazy(() => import('@/pages/SetupPage'))

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

          {/* SetupGate redirects a fresh install to onboarding; AppShell owns
              the page container, header, and Suspense boundary, so every route
              inside it renders with identical chrome. */}
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
        </Routes>
      </BrowserRouter>
    </I18nProvider>
  )
}
