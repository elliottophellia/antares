import * as React from 'react'
import * as SwitchPrimitive from '@radix-ui/react-switch'
import * as SeparatorPrimitive from '@radix-ui/react-separator'
import * as TabsPrimitive from '@radix-ui/react-tabs'
import * as TooltipPrimitive from '@radix-ui/react-tooltip'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

/* ---------- Card ---------- */

export function Card({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        'rounded-[var(--radius-lg)] border border-border bg-card text-card-foreground shadow-xs',
        className,
      )}
      {...props}
    />
  )
}

export function CardHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('flex flex-col gap-1.5 p-4 sm:p-5', className)} {...props} />
}

export function CardTitle({ className, ...props }: React.HTMLAttributes<HTMLHeadingElement>) {
  return <h3 className={cn('text-sm font-semibold tracking-tight sm:text-base', className)} {...props} />
}

export function CardDescription({ className, ...props }: React.HTMLAttributes<HTMLParagraphElement>) {
  return <p className={cn('text-xs text-muted-foreground sm:text-sm', className)} {...props} />
}

export function CardContent({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('p-4 pt-0 sm:p-5 sm:pt-0', className)} {...props} />
}

export function CardFooter({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('flex flex-wrap items-center gap-2 p-4 pt-0 sm:p-5 sm:pt-0', className)} {...props} />
}

/* ---------- Input / Textarea / Label ---------- */

export const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, ...props }, ref) => (
    <input
      ref={ref}
      className={cn(
        'flex h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 py-1 text-sm transition-colors',
        'placeholder:text-muted-foreground focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-50',
        // 16px on mobile prevents iOS Safari from zooming on focus.
        'text-base sm:text-sm',
        className,
      )}
      {...props}
    />
  ),
)
Input.displayName = 'Input'

export const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => (
  <textarea
    ref={ref}
    className={cn(
      'flex w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 py-2 text-base transition-colors sm:text-sm',
      'placeholder:text-muted-foreground focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-50',
      className,
    )}
    {...props}
  />
))
Textarea.displayName = 'Textarea'

export function Label({ className, ...props }: React.LabelHTMLAttributes<HTMLLabelElement>) {
  return (
    <label
      className={cn('text-xs font-medium text-foreground/90 sm:text-sm', className)}
      {...props}
    />
  )
}

/* ---------- Badge ---------- */

const badgeVariants = cva(
  'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium leading-4 whitespace-nowrap',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-primary/15 text-primary',
        secondary: 'border-transparent bg-secondary text-secondary-foreground',
        outline: 'border-border text-muted-foreground',
        success: 'border-transparent bg-[color-mix(in_oklch,var(--success)_18%,transparent)] text-[var(--success)]',
        warning: 'border-transparent bg-[color-mix(in_oklch,var(--warning)_18%,transparent)] text-[var(--warning)]',
        destructive: 'border-transparent bg-destructive/15 text-destructive',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export function Badge({
  className,
  variant,
  ...props
}: React.HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />
}

/* ---------- Switch ---------- */

export function Switch({ className, ...props }: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      className={cn(
        'peer inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors',
        'data-[state=checked]:bg-primary data-[state=unchecked]:bg-input disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb className="pointer-events-none block size-4 rounded-full bg-background shadow-sm transition-transform data-[state=checked]:translate-x-4 data-[state=unchecked]:translate-x-0" />
    </SwitchPrimitive.Root>
  )
}

/* ---------- Separator ---------- */

export function Separator({
  className,
  orientation = 'horizontal',
  ...props
}: React.ComponentProps<typeof SeparatorPrimitive.Root>) {
  return (
    <SeparatorPrimitive.Root
      orientation={orientation}
      className={cn(
        'shrink-0 bg-border',
        orientation === 'horizontal' ? 'h-px w-full' : 'h-full w-px',
        className,
      )}
      {...props}
    />
  )
}

/* ---------- Tabs ---------- */

export const Tabs = TabsPrimitive.Root

export function TabsList({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.List>) {
  return (
    <TabsPrimitive.List
      className={cn(
        'inline-flex h-9 items-center gap-1 overflow-x-auto rounded-[var(--radius-sm)] bg-muted p-1 text-muted-foreground',
        className,
      )}
      {...props}
    />
  )
}

export function TabsTrigger({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      className={cn(
        'inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-[var(--radius-xs)] px-3 py-1 text-xs font-medium transition-all',
        'data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm',
        'disabled:pointer-events-none disabled:opacity-50',
        className,
      )}
      {...props}
    />
  )
}

export function TabsContent({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Content>) {
  return <TabsPrimitive.Content className={cn('mt-4 outline-none', className)} {...props} />
}

/* ---------- Tooltip ---------- */

export const TooltipProvider = TooltipPrimitive.Provider

export function Tooltip({
  label,
  children,
  side = 'top',
}: {
  label: React.ReactNode
  children: React.ReactNode
  side?: 'top' | 'right' | 'bottom' | 'left'
}) {
  return (
    <TooltipPrimitive.Root>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content
          side={side}
          sideOffset={6}
          className="z-50 rounded-[var(--radius-xs)] border border-border bg-popover px-2 py-1 text-xs text-popover-foreground shadow-md"
        >
          {label}
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  )
}

/* ---------- Empty state ---------- */

export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
}: {
  icon?: React.ReactNode
  title: string
  description?: string
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-3 rounded-[var(--radius-lg)] border border-dashed border-border px-6 py-14 text-center',
        className,
      )}
    >
      {icon ? <div className="text-muted-foreground/70">{icon}</div> : null}
      <div className="space-y-1">
        <p className="text-sm font-medium">{title}</p>
        {description ? (
          <p className="mx-auto max-w-sm text-xs text-muted-foreground sm:text-sm">{description}</p>
        ) : null}
      </div>
      {action}
    </div>
  )
}

/* ---------- Page header ---------- */

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string
  description?: string
  actions?: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
      <div className="min-w-0 space-y-1.5">
        <h1 className="text-lg font-semibold leading-tight tracking-tight text-balance sm:text-xl">{title}</h1>
        {description ? <p className="text-xs text-muted-foreground sm:text-sm">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  )
}
