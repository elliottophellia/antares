import { useCallback, useState } from 'react'
import {
  ArrowSquareOut,
  CheckCircle,
  Download,
  EnvelopeSimple,
  Globe,
  Info,
  Key,
  Play,
  Robot,
  ShieldCheck,
  Stop,
  Trash,
  UserCircle,
  X,
} from '@phosphor-icons/react'
import { PageLayout } from '@/components/layout/PageLayout'
import { Card, CardContent, CardDescription, CardHeader, CardTitle, Badge, Input, Label, Switch, EmptyState } from '@/components/ui/primitives'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { SensitiveGate } from '@/components/ui/SensitiveGate'
import { useApi } from '@/lib/hooks'
import { post, del } from '@/lib/api'
import { useI18n } from '@/lib/i18n'

interface SocialStatus {
  enabled: boolean
  encryption_ready: boolean
  imap_configured: boolean
  imap_host: string
  imap_port: number
  imap_username: string
  browser: { enabled: boolean; state: string; error: string }
  autopilot_enabled: boolean
  accounts: SocialAccount[]
}

interface SocialAccount {
  id: string
  platform: string
  display_name: string
  username: string
  profile_url: string
  status: string
  rag_namespace: string
  skill_name: string
  last_checked_at: string | null
  created_at: string
  updated_at: string
  has_password: boolean
  has_recovery: boolean
}

export default function SocialMediaPage() {
  const { t } = useI18n()
  const { data: status, reload, loading } = useApi<SocialStatus>('/social/status')
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [showRecoveryKey, setShowRecoveryKey] = useState(false)
  const [recoveryKey, setRecoveryKey] = useState('')
  const [showImap, setShowImap] = useState(false)
  const [showAdd, setShowAdd] = useState(false)

  const handleAction = useCallback(async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key); setError('')
    try { await fn(); await reload() }
    catch (e) { setError((e as Error).message) }
    finally { setBusy('') }
  }, [reload])

  if (loading && !status) return <PageLayout><Skeleton className="h-32 w-full" /></PageLayout>

  const s = status ?? null

  return (
    <PageLayout>
      {error && <Card className="border-destructive/50"><CardContent className="py-3 text-sm text-destructive">{error}</CardContent></Card>}

      {s && !s.encryption_ready && (
        <Card className="border-warning/50">
          <CardHeader>
            <CardTitle className="text-sm">{t('social.onboarding.encryptionRequired')}</CardTitle>
          </CardHeader>
          <CardContent className="flex items-center justify-between">
            <p className="text-sm text-muted-foreground">{t('social.onboarding.description')}</p>
            <Button size="sm" loading={busy === 'encryption'} onClick={() => handleAction('encryption', async () => {
              const res = await post<{ recovery_key: string }>('/social/encryption/setup', {})
              setRecoveryKey(res.recovery_key); setShowRecoveryKey(true)
            })}>{t('social.onboarding.generateKey')}</Button>
          </CardContent>
        </Card>
      )}

      {showRecoveryKey && (
        <Card className="border-success/50">
          <CardHeader>
            <CardTitle className="text-sm">{t('social.onboarding.recoveryKeyTitle')}</CardTitle>
            <CardDescription>{t('social.onboarding.recoveryKeyDesc')}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <code className="block break-all rounded-[var(--radius-sm)] bg-muted p-3 text-sm">{recoveryKey}</code>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => {
                const blob = new Blob([recoveryKey], { type: 'text/plain' })
                const url = URL.createObjectURL(blob)
                const a = document.createElement('a'); a.href = url; a.download = 'antares-recovery-key.txt'; a.click()
                URL.revokeObjectURL(url)
              }}><Download className="mr-1.5 size-4" />{t('social.onboarding.recoveryKeyDownload')}</Button>
              <Button size="sm" variant="ghost" onClick={() => setShowRecoveryKey(false)}><X className="mr-1.5 size-4" />Close</Button>
            </div>
            <p className="text-xs text-muted-foreground">{t('social.onboarding.restartRequired')}</p>
          </CardContent>
        </Card>
      )}

      <SensitiveGate>
        <div className="grid gap-4 lg:grid-cols-2">
          {/* Gmail / IMAP */}
          <Card className="overflow-hidden bg-gradient-to-br from-card via-card to-primary/[0.025]">
            <CardHeader className="pb-4">
              <CardTitle className="flex items-center gap-3 text-sm">
                <span className="grid size-9 shrink-0 place-items-center rounded-[var(--radius-md)] border border-border bg-background text-primary shadow-xs">
                  <EnvelopeSimple className="size-[18px]" />
                </span>
                <span>{t('social.gmail.title')}</span>
                {s?.imap_configured ? <Badge className="ml-auto">{t('social.gmail.configured')}</Badge> : <Badge variant="secondary" className="ml-auto">{t('social.gmail.notConfigured')}</Badge>}
              </CardTitle>
              <CardDescription className="pl-12">{t('social.gmail.description')}</CardDescription>
            </CardHeader>
            <CardContent className="min-h-24">
              {showImap ? <IMAPForm defaults={s} onDone={() => { setShowImap(false); reload() }} onCancel={() => setShowImap(false)} /> : (
                <div className="space-y-4 text-sm">
                  {s?.imap_configured ? <>
                    <div className="grid gap-px overflow-hidden rounded-[var(--radius-md)] border border-border bg-border sm:grid-cols-2">
                      <div className="min-w-0 bg-background/80 px-3 py-2.5">
                        <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-muted-foreground">Host</p>
                        <p className="mt-1 truncate text-xs font-medium">{s.imap_host}:{s.imap_port}</p>
                      </div>
                      <div className="min-w-0 bg-background/80 px-3 py-2.5">
                        <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-muted-foreground">Email</p>
                        <p className="mt-1 truncate text-xs font-medium">{s.imap_username}</p>
                      </div>
                    </div>
                    <Button size="sm" variant="outline" onClick={() => setShowImap(true)}>Edit configuration</Button>
                  </> : <Button size="sm" onClick={() => setShowImap(true)}>{t('social.gmail.title')}</Button>}
                </div>
              )}
            </CardContent>
          </Card>

          {/* Browser */}
          <Card className="overflow-hidden bg-gradient-to-br from-card via-card to-primary/[0.025]">
            <CardHeader className="pb-4">
              <CardTitle className="flex items-center gap-3 text-sm">
                <span className="grid size-9 shrink-0 place-items-center rounded-[var(--radius-md)] border border-border bg-background text-primary shadow-xs">
                  <Globe className="size-[18px]" />
                </span>
                <span>{t('social.browser.title')}</span>
                <BrowserBadge state={s?.browser?.state ?? 'disabled'} />
              </CardTitle>
              <CardDescription className="pl-12">{t('social.browser.description')}</CardDescription>
            </CardHeader>
            <CardContent className="min-h-24">
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-[var(--radius-md)] border border-border bg-background/70 p-3">
                <div className="flex items-center gap-2 text-xs text-muted-foreground">
                  <ShieldCheck className="size-4 text-primary" />
                  Stable fingerprint and login sessions
                </div>
                {s?.browser?.state !== 'running' ? (
                  <Button size="sm" loading={busy === 'browser-start'} onClick={() => handleAction('browser-start', () => post('/social/browser/start', {}))} disabled={!s?.encryption_ready}>
                    <Play className="mr-1.5 size-4" />{t('social.browser.start')}
                  </Button>
                ) : (
                  <Button size="sm" variant="outline" loading={busy === 'browser-stop'} onClick={() => handleAction('browser-stop', () => post('/social/browser/stop', {}))}>
                    <Stop className="mr-1.5 size-4" />{t('social.browser.stop')}
                  </Button>
                )}
              </div>
              {s?.browser?.error && <p className="mt-2 text-xs text-destructive">{s.browser.error}</p>}
            </CardContent>
          </Card>
        </div>

        {/* Autopilot */}
        <Card className="overflow-hidden">
          <CardContent className="flex flex-col gap-4 p-4 sm:flex-row sm:items-center sm:justify-between sm:p-5">
            <div className="flex min-w-0 items-start gap-3">
              <span className="grid size-10 shrink-0 place-items-center rounded-full border border-border bg-primary/10 text-primary">
                <Robot className="size-5" />
              </span>
              <div>
                <CardTitle className="text-sm">{t('social.autopilot.title')}</CardTitle>
                <CardDescription className="mt-1 max-w-2xl">{t('social.autopilot.description')}</CardDescription>
              </div>
            </div>
            <label className="flex shrink-0 cursor-pointer items-center justify-between gap-4 rounded-full border border-border bg-muted/40 px-3 py-2 text-xs font-medium text-muted-foreground">
              {t('social.autopilot.toggle')}
              <Switch checked={s?.autopilot_enabled ?? false} disabled={busy === 'autopilot'} onCheckedChange={(v) => handleAction('autopilot', () => post('/social/autopilot', { enabled: v }))} />
            </label>
          </CardContent>
        </Card>

        {/* Accounts */}
        <div className="space-y-3">
          <div className="flex items-end justify-between gap-3 border-b border-border pb-3">
            <div>
              <h2 className="text-sm font-semibold">{t('social.accounts.title')}</h2>
              <p className="mt-0.5 text-xs text-muted-foreground">{s?.accounts?.length ?? 0} connected profile{(s?.accounts?.length ?? 0) === 1 ? '' : 's'}</p>
            </div>
            <Button size="sm" variant="outline" onClick={() => setShowAdd(true)} disabled={!s?.encryption_ready}>{t('social.accounts.add')}</Button>
          </div>
          {s?.accounts && s.accounts.length > 0 ? (
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {s.accounts.map((a) => <AccountCard key={a.id} acct={a} onRemoved={reload} />)}
            </div>
          ) : <EmptyState title={t('social.accounts.empty')} description={t('social.accounts.emptyDesc')} />}
        </div>

        <AddAccountDialog
          open={showAdd}
          onOpenChange={setShowAdd}
          onDone={() => { setShowAdd(false); reload() }}
        />
      </SensitiveGate>
    </PageLayout>
  )
}

function BrowserBadge({ state }: { state: string }) {
  const { t } = useI18n()
  const variant = state === 'running' ? 'default' : state === 'error' ? 'destructive' : 'secondary'
  return <Badge variant={variant as never} className="ml-auto">{t(`social.browser.state.${state}` as never) ?? state}</Badge>
}

function IMAPForm({ defaults, onDone, onCancel }: { defaults: SocialStatus | null; onDone: () => void; onCancel: () => void }) {
  const { t } = useI18n()
  const [host, setHost] = useState(defaults?.imap_host ?? 'imap.gmail.com')
  const [port, setPort] = useState(String(defaults?.imap_port ?? 993))
  const [username, setUsername] = useState(defaults?.imap_username ?? '')
  const [password, setPassword] = useState('')
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState('')
  const [saving, setSaving] = useState(false)

  const test = async () => {
    setTesting(true); setResult('')
    try {
      const res = await post<{ ok: boolean; error?: string; inbox_count?: number }>('/social/imap/test', { host, port: Number(port), username, password })
      setResult(res.ok ? t('social.gmail.testSuccess').replace('{count}', String(res.inbox_count ?? 0)) : t('social.gmail.testFail').replace('{error}', res.error ?? ''))
    } catch (e) { setResult((e as Error).message) } finally { setTesting(false) }
  }

  const save = async () => {
    setSaving(true)
    try { await post('/social/imap/save', { host, port: Number(port), username, password }); onDone() }
    catch (e) { setResult((e as Error).message) } finally { setSaving(false) }
  }

  return (
    <div className="space-y-2">
      <div className="grid grid-cols-3 gap-2">
        <div className="col-span-2 space-y-1"><Label>{t('social.gmail.host')}</Label><Input value={host} onChange={(e) => setHost(e.target.value)} /></div>
        <div className="space-y-1"><Label>{t('social.gmail.port')}</Label><Input value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" /></div>
      </div>
      <div className="space-y-1"><Label>{t('social.gmail.username')}</Label><Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="user@gmail.com" /></div>
      <div className="space-y-1"><Label>{t('social.gmail.password')}</Label><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="App Password" /></div>
      <details className="rounded-[var(--radius-sm)] border border-border bg-muted/50 p-3 text-xs">
        <summary className="flex cursor-pointer items-center gap-1.5 font-medium text-muted-foreground">
          <Info className="size-3.5" />
          {t('social.gmail.appPasswordTutorial')}
        </summary>
        <ol className="mt-2 list-decimal space-y-1 pl-4 text-muted-foreground">
          <li>{t('social.gmail.tutorialStep1')}</li>
          <li>{t('social.gmail.tutorialStep2')}</li>
          <li>{t('social.gmail.tutorialStep3')}</li>
          <li>{t('social.gmail.tutorialStep4')}</li>
          <li>{t('social.gmail.tutorialStep5')}</li>
        </ol>
      </details>
      {result && <p className="text-xs text-muted-foreground">{result}</p>}
      <div className="flex gap-2">
        <Button size="sm" variant="outline" loading={testing} onClick={test} disabled={!username || !password}>{t('social.gmail.test')}</Button>
        <Button size="sm" loading={saving} onClick={save} disabled={!username}>{t('social.gmail.save')}</Button>
        <Button size="sm" variant="ghost" onClick={onCancel}>Cancel</Button>
      </div>
    </div>
  )
}

function AccountCard({ acct, onRemoved }: { acct: SocialAccount; onRemoved: () => void }) {
  const { t } = useI18n()
  const [removing, setRemoving] = useState(false)
  const remove = async () => { setRemoving(true); try { await del(`/social/accounts/${acct.id}`); onRemoved() } catch { setRemoving(false) } }
  const name = acct.display_name || acct.username
  const initial = name.trim().charAt(0).toUpperCase() || '?'
  const platform = acct.platform.trim().toLowerCase()
  const connected = acct.status === 'connected'
  return (
    <Card className="group relative flex min-h-56 flex-col overflow-hidden transition-colors hover:border-primary/35">
      <div className="absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-primary/70 to-transparent opacity-0 transition-opacity group-hover:opacity-100" />
      <CardContent className="flex flex-1 flex-col p-4 sm:p-4">
        <div className="flex items-start gap-3">
          <div className="relative grid size-11 shrink-0 place-items-center rounded-[var(--radius-md)] border border-primary/20 bg-primary/10 text-base font-semibold text-primary shadow-xs">
            {initial}
            <span className={`absolute -bottom-1 -right-1 size-3 rounded-full border-2 border-card ${connected ? 'bg-emerald-500' : 'bg-muted-foreground'}`} />
          </div>
          <div className="min-w-0 flex-1 pt-0.5">
            <div className="flex items-start gap-2">
              <p className="min-w-0 flex-1 truncate font-semibold leading-5">{name}</p>
              <Badge variant={connected ? 'default' : 'secondary'} className="shrink-0">
                {t(`social.accounts.status.${acct.status}` as never) ?? acct.status}
              </Badge>
            </div>
            <p className="mt-0.5 truncate text-xs text-muted-foreground">@{acct.username}</p>
          </div>
        </div>

        <div className="mt-4 flex flex-wrap gap-1.5">
          <span className="rounded-full border border-border bg-muted/40 px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">{platform}</span>
          {acct.has_password && <span className="inline-flex items-center gap-1 rounded-full border border-border bg-muted/40 px-2 py-1 text-[10px] text-muted-foreground"><Key className="size-3" />{t('social.accounts.hasPassword')}</span>}
          {acct.has_recovery && <span className="inline-flex items-center gap-1 rounded-full border border-border bg-muted/40 px-2 py-1 text-[10px] text-muted-foreground"><ShieldCheck className="size-3" />{t('social.accounts.hasRecovery')}</span>}
        </div>

        <div className="mt-auto pt-4">
          {acct.profile_url ? (
            <a
              href={acct.profile_url}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-2 rounded-[var(--radius-md)] border border-border bg-background/60 px-3 py-2 text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary"
              title={acct.profile_url}
            >
              <UserCircle className="size-4 shrink-0" />
              <span className="min-w-0 flex-1 truncate">Open profile</span>
              <ArrowSquareOut className="size-3.5 shrink-0" />
            </a>
          ) : (
            <div className="flex items-center gap-2 rounded-[var(--radius-md)] border border-dashed border-border px-3 py-2 text-xs text-muted-foreground">
              <UserCircle className="size-4" /> No profile URL
            </div>
          )}
        </div>
      </CardContent>
      <div className="flex items-center justify-between border-t border-border bg-muted/15 px-3 py-2">
        <span className="inline-flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <CheckCircle className={`size-3.5 ${connected ? 'text-emerald-500' : ''}`} />
          Credentials encrypted
        </span>
        <Button size="sm" variant="ghost" className="h-8 px-2 text-muted-foreground hover:text-destructive" loading={removing} onClick={remove}><Trash className="mr-1.5 size-3.5" />{t('social.accounts.remove')}</Button>
      </div>
    </Card>
  )
}

function AddAccountDialog({ open, onOpenChange, onDone }: { open: boolean; onOpenChange: (open: boolean) => void; onDone: () => void }) {
  const { t } = useI18n()
  const [platform, setPlatform] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [recovery, setRecovery] = useState('')
  const [profileUrl, setProfileUrl] = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  const save = async () => {
    setSaving(true); setErr('')
    try { await post('/social/accounts', { platform, username, password, recovery_codes: recovery, profile_url: profileUrl, status: 'connected' }); onDone() }
    catch (e) { setErr((e as Error).message) } finally { setSaving(false) }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!saving) onOpenChange(next) }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('social.accounts.add')}</DialogTitle>
          <p className="text-xs text-muted-foreground">Store an account for agents to manage. Credentials are encrypted locally.</p>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5"><Label>{t('social.accounts.platform')}</Label><Input autoFocus value={platform} onChange={(e) => setPlatform(e.target.value)} placeholder="instagram" /></div>
            <div className="space-y-1.5"><Label>{t('social.accounts.username')}</Label><Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="username" /></div>
          </div>
          <div className="space-y-1.5"><Label>{t('social.accounts.password')}</Label><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" /></div>
          <div className="space-y-1.5"><Label>{t('social.accounts.recovery')}</Label><Input value={recovery} onChange={(e) => setRecovery(e.target.value)} placeholder="Optional recovery codes" /></div>
          <div className="space-y-1.5"><Label>{t('social.accounts.profileUrl')}</Label><Input type="url" value={profileUrl} onChange={(e) => setProfileUrl(e.target.value)} placeholder="https://..." /></div>
          {err && <p role="alert" className="rounded-[var(--radius-sm)] border border-destructive/30 bg-destructive/5 p-2.5 text-xs text-destructive">{err}</p>}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={saving}>Cancel</Button>
          <Button loading={saving} onClick={save} disabled={!platform.trim() || !username.trim()}>Save account</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
