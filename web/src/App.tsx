import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/layout/AppShell'
import { ROUTES } from '@/lib/routes'
import { I18nProvider } from '@/lib/i18n'

export function App() {
  return (
    <I18nProvider>
      <BrowserRouter>
        <Routes>
          {/* The shell owns the page container, header, and Suspense boundary,
              so every route renders with identical chrome. */}
          <Route element={<AppShell />}>
            {ROUTES.flatMap(({ path, aliases, component: Component }) =>
              [path, ...(aliases ?? [])].map((p) => (
                <Route key={p} path={p} element={<Component />} />
              )),
            )}
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </I18nProvider>
  )
}
