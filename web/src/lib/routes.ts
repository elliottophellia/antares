import { lazy, type ComponentType, type LazyExoticComponent } from 'react'
import {
  ChartLineUp,
  ChatCircleDots,
  ClockCounterClockwise,
  Database,
  FileText,
  Fingerprint,
  Gear,
  GlobeHemisphereWest,
  HardDrives,
  Plugs,
  PlugsConnected,
  PuzzlePiece,
  UsersThree,
  Broadcast,
  Kanban,
  Robot,
  ShieldCheck,
  Sparkle,
  Terminal,
  Toolbox,
} from '@phosphor-icons/react'
import type { MessageKey } from './i18n'

export type IconComponent = ComponentType<{
  className?: string
  weight?: 'regular' | 'fill' | 'bold' | 'duotone'
}>

export interface RouteDef {
  path: string
  /** Extra paths that render the same page (e.g. /c/:id for chat). */
  aliases?: string[]
  titleKey: MessageKey
  descKey?: MessageKey
  icon: IconComponent
  component: LazyExoticComponent<ComponentType>
  /** Shown in the mobile bottom bar. */
  primary?: boolean
  /** Renders without the standard page container and header. */
  fullBleed?: boolean
  /**
   * Opt out of the default fixed-height frame: let this page size to its
   * content and scroll with the document instead of scrolling inside a fixed
   * container. For genuinely short, static pages.
   */
  staticHeight?: boolean
}

/**
 * The single source of truth for navigation, routing, and page chrome.
 * Pages render content only — the shell owns the container and header so every
 * screen shares identical spacing.
 */
export const ROUTES: RouteDef[] = [
  {
    path: '/',
    aliases: ['/c/:sessionId'],
    titleKey: 'nav.chat',
    icon: ChatCircleDots,
    component: lazy(() => import('@/pages/ChatPage')),
    primary: true,
    fullBleed: true,
  },
  {
    path: '/sessions',
    titleKey: 'sessions.title',
    descKey: 'sessions.desc',
    icon: ClockCounterClockwise,
    component: lazy(() => import('@/pages/SessionsPage')),
    primary: true,
  },
  {
    path: '/providers',
    aliases: ['/models'],
    titleKey: 'providers.title',
    descKey: 'providers.desc',
    icon: Plugs,
    component: lazy(() => import('@/pages/ProvidersPage')),
    primary: true,
  },
  {
    path: '/tools',
    titleKey: 'tools.title',
    descKey: 'tools.desc',
    icon: Toolbox,
    component: lazy(() => import('@/pages/ToolsPage')),
  },
  {
    path: '/memory',
    titleKey: 'memory.title',
    descKey: 'memory.desc',
    icon: Database,
    component: lazy(() => import('@/pages/MemoryPage')),
  },
  {
    path: '/roles',
    titleKey: 'roles.title',
    descKey: 'roles.desc',
    icon: UsersThree,
    component: lazy(() => import('@/pages/RolesPage')),
  },
  {
    path: '/soul',
    titleKey: 'soul.title',
    descKey: 'soul.desc',
    icon: Fingerprint,
    component: lazy(() => import('@/pages/SoulPage')),
    staticHeight: true,
  },
  {
    path: '/skills',
    titleKey: 'skills.title',
    descKey: 'skills.desc',
    icon: Sparkle,
    component: lazy(() => import('@/pages/SkillsPage')),
  },
  {
    path: '/cron',
    titleKey: 'cron.title',
    descKey: 'cron.desc',
    icon: ClockCounterClockwise,
    component: lazy(() => import('@/pages/CronPage')),
  },
  {
    path: '/channels',
    titleKey: 'channels.title',
    descKey: 'channels.desc',
    icon: Plugs,
    component: lazy(() => import('@/pages/ChannelsPage')),
  },
  {
    path: '/autopilot',
    titleKey: 'autopilot.title',
    descKey: 'autopilot.desc',
    icon: Robot,
    component: lazy(() => import('@/pages/AutopilotPage')),
  },
  {
    path: '/engagement',
    titleKey: 'engagement.title',
    descKey: 'engagement.desc',
    icon: ShieldCheck,
    component: lazy(() => import('@/pages/EngagementPage')),
  },
  {
    path: '/board',
    titleKey: 'board.title',
    descKey: 'board.desc',
    icon: Kanban,
    component: lazy(() => import('@/pages/BoardPage')),
  },
  {
    path: '/intercept',
    titleKey: 'intercept.title',
    descKey: 'intercept.desc',
    icon: Broadcast,
    component: lazy(() => import('@/pages/InterceptPage')),
  },
  {
    path: '/mcp',
    titleKey: 'mcp.title',
    descKey: 'mcp.desc',
    icon: PlugsConnected,
    component: lazy(() => import('@/pages/McpPage')),
  },
  {
    path: '/plugins',
    titleKey: 'plugins.title',
    descKey: 'plugins.desc',
    icon: PuzzlePiece,
    component: lazy(() => import('@/pages/PluginsPage')),
  },
  {
    path: '/proxies',
    titleKey: 'proxies.title',
    descKey: 'proxies.desc',
    icon: GlobeHemisphereWest,
    component: lazy(() => import('@/pages/ProxiesPage')),
  },
  {
    path: '/vps',
    titleKey: 'vps.title',
    descKey: 'vps.desc',
    icon: HardDrives,
    component: lazy(() => import('@/pages/VPSPage')),
  },
  {
    path: '/analytics',
    titleKey: 'analytics.title',
    descKey: 'analytics.desc',
    icon: ChartLineUp,
    component: lazy(() => import('@/pages/AnalyticsPage')),
  },
  {
    path: '/files',
    titleKey: 'files.title',
    descKey: 'files.desc',
    icon: FileText,
    component: lazy(() => import('@/pages/FilesPage')),
  },
  {
    path: '/logs',
    titleKey: 'logs.title',
    descKey: 'logs.desc',
    icon: Terminal,
    component: lazy(() => import('@/pages/LogsPage')),
  },
  {
    path: '/config',
    titleKey: 'config.title',
    descKey: 'config.desc',
    icon: Gear,
    component: lazy(() => import('@/pages/ConfigPage')),
    primary: true,
  },
  {
    path: '/system',
    titleKey: 'system.title',
    descKey: 'system.desc',
    icon: Robot,
    component: lazy(() => import('@/pages/SystemPage')),
  },
]

/** Nav entries use the short sidebar labels rather than the page titles. */
export const NAV_LABELS: Record<string, MessageKey> = {
  '/': 'nav.chat',
  '/sessions': 'nav.sessions',
  '/providers': 'nav.providers',
  '/tools': 'nav.tools',
  '/memory': 'nav.memory',
  '/skills': 'nav.skills',
  '/roles': 'nav.roles',
  '/soul': 'nav.soul',
  '/cron': 'nav.cron',
  '/channels': 'nav.channels',
  '/autopilot': 'nav.autopilot',
  '/engagement': 'nav.engagement',
  '/board': 'nav.board',
  '/intercept': 'nav.intercept',
  '/mcp': 'nav.mcp',
  '/plugins': 'nav.plugins',
  '/proxies': 'nav.proxies',
  '/vps': 'nav.vps',
  '/analytics': 'nav.analytics',
  '/files': 'nav.files',
  '/logs': 'nav.logs',
  '/config': 'nav.config',
  '/system': 'nav.system',
}

export const PRIMARY_ROUTES = ROUTES.filter((r) => r.primary)

/** Resolve the route definition for a pathname. */
export function routeFor(pathname: string): RouteDef | undefined {
  const exact = ROUTES.find((r) => r.path === pathname)
  if (exact) return exact
  // Alias segments are matched by their static prefix (e.g. /c/<id>).
  return ROUTES.find((r) =>
    r.aliases?.some((a) => {
      const prefix = a.split('/:')[0]
      return prefix !== '' && pathname.startsWith(prefix + '/')
    }),
  )
}
