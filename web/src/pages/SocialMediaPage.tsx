import { useCallback, useState } from 'react'
import { Download, EnvelopeSimple, Globe, Play, Stop, Trash, X } from '@phosphor-icons/react'
import { PageLayout } from '@/components/layout/PageLayout'
import { Card, CardContent, CardDescription, CardHeader, CardTitle, Badge, Input, Label, Switch, EmptyState } from '@/components/ui/primitives'
import { Button } from '@/components/ui/button'
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
        <div className="grid gap-4 sm:grid-cols-2">
          {/* Gmail / IMAP */}
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-sm">
                <EnvelopeSimple className="size-4" />{t('social.gmail.title')}
                {s?.imap_configured ? <Badge className="ml-auto">{t('social.gmail.configured')}</Badge> : <Badge variant="secondary" className="ml-auto">{t('social.gmail.notConfigured')}</Badge>}
              </CardTitle>
              <CardDescription>{t('social.gmail.description')}</CardDescription>
            </CardHeader>
            <CardContent>
              {showImap ? <IMAPForm defaults={s} onDone={() => { setShowImap(false); reload() }} onCancel={() => setShowImap(false)} /> : (
                <div className="space-y-2 text-sm">
                  {s?.imap_configured ? <>
                    <div className="flex justify-between"><span className="text-muted-foreground">Host</span><span>{s.imap_host}:{s.imap_port}</span></div>
                    <div className="flex justify-between"><span className="text-muted-foreground">Email</span><span className="truncate">{s.imap_username}</span></div>
                    <Button size="sm" variant="outline" onClick={() => setShowImap(true)}>Edit</Button>
                  </> : <Button size="sm" onClick={() => setShowImap(true)}>{t('social.gmail.title')}</Button>}
                </div>
              )}
            </CardContent>
          </Card>

          {/* Browser */}
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-sm">
                <Globe className="size-4" />{t('social.browser.title')}
                <BrowserBadge state={s?.browser?.state ?? 'disabled'} />
              </CardTitle>
              <CardDescription>{t('social.browser.description')}</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="flex flex-wrap gap-2">
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
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">{t('social.autopilot.title')}</CardTitle>
            <CardDescription>{t('social.autopilot.description')}</CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between">
            <span className="text-sm text-muted-foreground">{t('social.autopilot.toggle')}</span>
            <Switch checked={s?.autopilot_enabled ?? false} disabled={busy === 'autopilot'} onCheckedChange={(v) => handleAction('autopilot', () => post('/social/autopilot', { enabled: v }))} />
          </CardContent>
        </Card>

        {/* Accounts */}
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold">{t('social.accounts.title')}</h2>
            <Button size="sm" variant="outline" onClick={() => setShowAdd(true)} disabled={!s?.encryption_ready}>{t('social.accounts.add')}</Button>
          </div>
          {s?.accounts && s.accounts.length > 0 ? (
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {s.accounts.map((a) => <AccountCard key={a.id} acct={a} onRemoved={reload} />)}
            </div>
          ) : <EmptyState title={t('social.accounts.empty')} description={t('social.accounts.emptyDesc')} />}
        </div>

        {showAdd && <AddForm onDone={() => { setShowAdd(false); reload() }} onCancel={() => setShowAdd(false)} />}
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
      <div className="space-y-1"><Label>{t('social.gmail.password')}</Label><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} /></div>
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
  return (
    <Card className="flex flex-col">
      <CardContent className="flex-1 p-3.5">
        <div className="flex items-start justify-between">
          <div className="min-w-0">
            <p className="truncate font-medium">{acct.display_name || acct.username}</p>
            <p className="truncate text-xs text-muted-foreground">@{acct.username} · {acct.platform}</p>
          </div>
          <Badge variant={acct.status === 'connected' ? 'default' : 'secondary'}>{t(`social.accounts.status.${acct.status}` as never) ?? acct.status}</Badge>
        </div>
        <div className="mt-2 flex flex-wrap gap-2 text-xs text-muted-foreground">
          {acct.has_password && <span>{t('social.accounts.hasPassword')}</span>}
          {acct.has_recovery && <span>· {t('social.accounts.hasRecovery')}</span>}
        </div>
        {acct.profile_url && <a href={acct.profile_url} target="_blank" rel="noopener noreferrer" className="mt-1 block truncate text-xs text-primary hover:underline">{acct.profile_url}</a>}
      </CardContent>
      <div className="flex border-t border-border p-3 pt-2">
        <Button size="sm" variant="ghost" className="text-muted-foreground hover:text-destructive" loading={removing} onClick={remove}><Trash className="mr-1.5 size-4" />{t('social.accounts.remove')}</Button>
      </div>
    </Card>
  )
}

function AddForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const { t } = useI18n()
  const [platform, setPlatform] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [, setRecovery] = useState('')
  const [profileUrl, setProfileUrl] = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  const save = async () => {
    setSaving(true); setErr('')
    try { await post('/social/accounts', { platform, username, password, recovery_codes: '', profile_url: profileUrl, status: 'connected' }); onDone() }
    catch (e) { setErr((e as Error).message) } finally { setSaving(false) }
  }

  return (
    <Card>
      <CardHeader><CardTitle className="text-sm">{t('social.accounts.add')}</CardTitle></CardHeader>
      <CardContent className="space-y-2">
        <div className="grid grid-cols-2 gap-2">
          <div className="space-y-1"><Label>{t('social.accounts.platform')}</Label><Input value={platform} onChange={(e) => setPlatform(e.target.value)} placeholder="instagram" /></div>
          <div className="space-y-1"><Label>{t('social.accounts.username')}</Label><Input value={username} onChange={(e) => setUsername(e.target.value)} /></div>
        </div>
        <div className="space-y-1"><Label>{t('social.accounts.password')}</Label><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} /></div>
        <div className="space-y-1"><Label>{t('social.accounts.recovery')}</Label><Input onChange={(e) => setRecovery(e.target.value)} /></div>
        <div className="space-y-1"><Label>{t('social.accounts.profileUrl')}</Label><Input value={profileUrl} onChange={(e) => setProfileUrl(e.target.value)} /></div>
        {err && <p className="text-xs text-destructive">{err}</p>}
        <div className="flex gap-2">
          <Button size="sm" loading={saving} onClick={save} disabled={!platform || !username}>Save</Button>
          <Button size="sm" variant="ghost" onClick={onCancel}>Cancel</Button>
        </div>
      </CardContent>
    </Card>
  )
}
