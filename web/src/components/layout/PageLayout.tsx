import { cn } from '@/lib/utils'

/**
 * The standard content layout: an optional sticky header (search / filters /
 * tabs that must stay in view), a scrollable body (the list), and an optional
 * sticky footer (usually pagination). Only the body scrolls; header and footer
 * stay put.
 *
 * Mobile-first. The page frame (PageFrame, with `fill`) fixes the outer height
 * and hides overflow on desktop, so the body simply takes the remaining height
 * and scrolls inside it. On mobile the same structure degrades to a natural
 * column — body grows, header/footer bracket it — so nothing is trapped
 * off-screen behind the keyboard or the bottom nav.
 */
export function PageLayout({
  header,
  children,
  footer,
  bodyClassName,
}: {
  // Stays fixed above the scroll area: search box, filter bar, tab strip.
  header?: React.ReactNode
  children: React.ReactNode
  footer?: React.ReactNode
  bodyClassName?: string
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {header ? <div className="shrink-0 space-y-3 pb-3">{header}</div> : null}
      <div className={cn('min-h-0 flex-1 space-y-6 lg:overflow-y-auto lg:pr-1', bodyClassName)}>
        {children}
      </div>
      {footer ? (
        <div className="mt-3 shrink-0 border-t border-border bg-background pt-3">{footer}</div>
      ) : null}
    </div>
  )
}
