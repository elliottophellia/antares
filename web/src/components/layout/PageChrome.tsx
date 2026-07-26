import { createContext, useContext, useEffect, useMemo, useState } from 'react'

type ActionNode = React.ReactNode

interface PageChromeValue {
  actions: ActionNode
  setActions: (node: ActionNode) => void
}

const PageChromeContext = createContext<PageChromeValue | null>(null)

/**
 * Lets a page contribute buttons to the shell-owned page header without
 * re-implementing the header itself.
 */
export function PageChromeProvider({ children }: { children: React.ReactNode }) {
  const [actions, setActions] = useState<ActionNode>(null)
  const value = useMemo(() => ({ actions, setActions }), [actions])
  return <PageChromeContext.Provider value={value}>{children}</PageChromeContext.Provider>
}

export function usePageChrome(): PageChromeValue {
  const ctx = useContext(PageChromeContext)
  if (!ctx) throw new Error('usePageChrome must be used inside PageChromeProvider')
  return ctx
}

/**
 * Register header actions for the current page. Pass the rendered nodes and a
 * dependency list, exactly like useEffect.
 */
export function usePageActions(node: ActionNode, deps: unknown[] = []) {
  const { setActions } = usePageChrome()
  useEffect(() => {
    setActions(node)
    return () => setActions(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
}
