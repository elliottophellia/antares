import { Suspense, useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { List, Moon, Sun, Translate, X } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { useLocalStorage, useMediaQuery } from '@/lib/hooks'
import { LANGUAGES, useI18n } from '@/lib/i18n'
import { NAV_LABELS, PRIMARY_ROUTES, ROUTES, routeFor } from '@/lib/routes'
import { Button } from '@/components/ui/button'
import { TooltipProvider } from '@/components/ui/primitives'
import { StatusPill } from '@/components/layout/StatusPill'
import { PageChromeProvider, usePageChrome } from '@/components/layout/PageChrome'
import { SkeletonList, SkeletonStats } from '@/components/ui/skeleton'

/** Applies the persisted theme to <html>. */
function useTheme() {
  const [theme, setTheme] = useLocalStorage<'dark' | 'light'>('antares.theme', 'dark')
  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
    document.documentElement.style.colorScheme = theme
  }, [theme])
  return { theme, setTheme }
}

function AntaresMark({ className }: { className?: string }) {
  return (
    <span
      className={cn(
        'grid size-8 shrink-0 place-items-center rounded-[var(--radius-sm)] bg-primary text-primary-foreground',
        className,
      )}
      aria-hidden
    >
      <svg viewBox="0 0 24 24" className="size-4.5" fill="none" stroke="currentColor" strokeWidth="2">
        <path
          d="M12 2.5 14.6 9l6.9.4-5.3 4.4 1.7 6.7L12 16.8 6.1 20.5l1.7-6.7L2.5 9.4 9.4 9Z"
          strokeLinejoin="round"
        />
      </svg>
    </span>
  )
}

function NavItems({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useI18n()
  return (
    <nav className="flex flex-col gap-0.5">
      {ROUTES.map(({ path, icon: Icon }) => (
        <NavLink
          key={path}
          to={path}
          end={path === '/'}
          onClick={onNavigate}
          className={({ isActive }) =>
            cn(
              'flex items-center gap-2.5 rounded-[var(--radius-sm)] px-3 py-2 text-sm transition-colors',
              isActive
                ? 'bg-primary/12 font-medium text-primary'
                : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
            )
          }
        >
          {({ isActive }) => (
            <>
              <Icon className="size-4.5 shrink-0" weight={isActive ? 'fill' : 'regular'} />
              <span className="truncate">{t(NAV_LABELS[path])}</span>
            </>
          )}
        </NavLink>
      ))}
    </nav>
  )
}

function LanguagePicker() {
  const { lang, setLang, t } = useI18n()
  return (
    <label className="flex h-9 items-center gap-2 rounded-[var(--radius-sm)] px-3 text-xs text-muted-foreground">
      <Translate className="size-4 shrink-0" />
      <span className="sr-only">{t('nav.language')}</span>
      <select
        value={lang}
        onChange={(e) => setLang(e.target.value as typeof lang)}
        className="w-full cursor-pointer bg-transparent text-xs text-foreground outline-none"
        aria-label={t('nav.language')}
      >
        {LANGUAGES.map((l) => (
          <option key={l.code} value={l.code} className="bg-popover text-popover-foreground">
            {l.flag} {l.label}
          </option>
        ))}
      </select>
    </label>
  )
}

function SidebarFooter() {
  const { theme, setTheme } = useTheme()
  const { t } = useI18n()
  return (
    <div className="space-y-1 border-t border-border p-3">
      <StatusPill />
      <LanguagePicker />
      <Button
        variant="ghost"
        size="sm"
        className="h-9 w-full justify-start gap-2 px-3"
        onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
      >
        {theme === 'dark' ? <Sun className="size-4" /> : <Moon className="size-4" />}
        {theme === 'dark' ? t('theme.light') : t('theme.dark')}
      </Button>
    </div>
  )
}

/** Route-level loading state; every page also has its own inner skeletons. */
function RouteFallback() {
  return (
    <div className="space-y-6">
      <SkeletonStats count={3} />
      <SkeletonList count={4} />
    </div>
  )
}

/**
 * Standard page frame: identical container width, padding, and header for every
 * route. Pages render content only.
 */
function PageFrame() {
  const location = useLocation()
  const { t } = useI18n()
  const { actions } = usePageChrome()
  const route = routeFor(location.pathname)

  if (route?.fullBleed) {
    return (
      <Suspense fallback={<div className="p-6"><RouteFallback /></div>}>
        <Outlet />
      </Suspense>
    )
  }

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-5 sm:px-6 sm:py-7 lg:px-8">
      {route ? (
        <header className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
          <div className="min-w-0 space-y-1.5">
            <h1 className="text-lg font-semibold leading-tight tracking-tight text-balance sm:text-xl">
              {t(route.titleKey)}
            </h1>
            {route.descKey ? (
              <p className="max-w-2xl text-xs leading-relaxed text-muted-foreground sm:text-sm">
                {t(route.descKey)}
              </p>
            ) : null}
          </div>
          {actions ? (
            <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>
          ) : null}
        </header>
      ) : null}

      <Suspense fallback={<RouteFallback />}>
        <Outlet />
      </Suspense>
    </div>
  )
}

export function AppShell() {
  const { theme, setTheme } = useTheme()
  const { t } = useI18n()
  const isDesktop = useMediaQuery('(min-width: 1024px)')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const location = useLocation()

  useEffect(() => setDrawerOpen(false), [location.pathname])

  return (
    <TooltipProvider delayDuration={300}>
      <PageChromeProvider>
        <div className="flex min-h-dvh bg-background">
          {/* Desktop sidebar */}
          <aside className="sticky top-0 hidden h-dvh w-60 shrink-0 flex-col border-r border-border bg-sidebar lg:flex">
            <div className="flex items-center gap-2.5 px-4 py-4">
              <AntaresMark />
              <div className="min-w-0">
                <p className="truncate text-sm font-semibold tracking-tight">Antares</p>
                <p className="truncate text-[11px] text-muted-foreground">{t('nav.subtitle')}</p>
              </div>
            </div>
            <div className="flex-1 overflow-y-auto px-2 pb-2">
              <NavItems />
            </div>
            <SidebarFooter />
          </aside>

          {/* Mobile drawer */}
          {!isDesktop && drawerOpen ? (
            <div className="fixed inset-0 z-50 lg:hidden">
              <button
                aria-label={t('nav.closeMenu')}
                className="absolute inset-0 bg-black/60 backdrop-blur-[2px]"
                onClick={() => setDrawerOpen(false)}
              />
              <div className="absolute inset-y-0 left-0 flex w-[17rem] max-w-[85vw] flex-col bg-sidebar shadow-2xl fade-up">
                <div className="safe-top flex items-center justify-between px-4 py-4">
                  <div className="flex items-center gap-2.5">
                    <AntaresMark />
                    <p className="text-sm font-semibold tracking-tight">Antares</p>
                  </div>
                  <Button variant="ghost" size="icon-sm" onClick={() => setDrawerOpen(false)}>
                    <X />
                  </Button>
                </div>
                <div className="flex-1 overflow-y-auto px-2 pb-4">
                  <NavItems onNavigate={() => setDrawerOpen(false)} />
                </div>
                <div className="safe-bottom">
                  <SidebarFooter />
                </div>
              </div>
            </div>
          ) : null}

          {/* Main column */}
          <div className="flex min-w-0 flex-1 flex-col">
            <header className="safe-top sticky top-0 z-30 flex h-14 items-center gap-2 border-b border-border bg-background/85 px-3 backdrop-blur-md lg:hidden">
              <Button
                variant="ghost"
                size="icon"
                onClick={() => setDrawerOpen(true)}
                aria-label={t('nav.openMenu')}
              >
                <List />
              </Button>
              <div className="flex min-w-0 items-center gap-2">
                <AntaresMark className="size-7" />
                <span className="truncate text-sm font-semibold">Antares</span>
              </div>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t('theme.toggle')}
                onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
                className="ml-auto"
              >
                {theme === 'dark' ? <Sun /> : <Moon />}
              </Button>
            </header>

            <main className="min-w-0 flex-1 pb-[4.5rem] lg:pb-0">
              <PageFrame />
            </main>

            {/* Mobile bottom navigation */}
            <nav className="safe-bottom fixed inset-x-0 bottom-0 z-30 flex border-t border-border bg-background/95 backdrop-blur-md lg:hidden">
              {PRIMARY_ROUTES.map(({ path, icon: Icon }) => (
                <NavLink
                  key={path}
                  to={path}
                  end={path === '/'}
                  className={({ isActive }) =>
                    cn(
                      'flex flex-1 flex-col items-center gap-1 px-1 py-2.5 text-[10px] font-medium leading-none transition-colors',
                      isActive ? 'text-primary' : 'text-muted-foreground',
                    )
                  }
                >
                  {({ isActive }) => (
                    <>
                      <Icon className="size-5" weight={isActive ? 'fill' : 'regular'} />
                      <span className="max-w-full truncate">{t(NAV_LABELS[path])}</span>
                    </>
                  )}
                </NavLink>
              ))}
            </nav>
          </div>
        </div>
      </PageChromeProvider>
    </TooltipProvider>
  )
}

/**
 * Vertical rhythm for page content. Every page wraps its sections in this so
 * the gap between blocks is identical everywhere.
 */
export function PageBody({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn('space-y-6', className)}>{children}</div>
}
