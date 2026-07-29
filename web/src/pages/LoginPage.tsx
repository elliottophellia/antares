import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Eye, EyeSlash, Lock } from '@phosphor-icons/react'
import { get, post, ApiError } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle, Input, Label } from '@/components/ui/primitives'

interface AuthStatus {
  password_required?: boolean
  authenticated?: boolean
}

/**
 * The dashboard login. Shown only when a dashboard password is configured. On
 * success the server sets an HTTP-only session cookie and we return to the app.
 */
export default function LoginPage() {
  const { t } = useI18n()
  const navigate = useNavigate()
  const [password, setPassword] = useState('')
  const [show, setShow] = useState(false)
  const [error, setError] = useState<string | undefined>()
  const [busy, setBusy] = useState(false)

  // If a login is not required (or already done), do not linger on this page.
  useEffect(() => {
    let alive = true
    get<AuthStatus>('/auth/status')
      .then((s) => {
        if (!alive) return
        if (!s.password_required || s.authenticated) navigate('/', { replace: true })
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [navigate])

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!password || busy) return
    setBusy(true)
    setError(undefined)
    try {
      await post('/auth/login', { password })
      navigate('/', { replace: true })
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t('login.failed'))
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-dvh items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <div className="flex items-center gap-2">
            <Lock size={20} weight="duotone" className="text-primary" />
            <CardTitle>{t('login.title')}</CardTitle>
          </div>
          <CardDescription>{t('login.desc')}</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="dash-password">{t('login.password')}</Label>
              <div className="relative">
                <Input
                  id="dash-password"
                  type={show ? 'text' : 'password'}
                  value={password}
                  autoFocus
                  autoComplete="current-password"
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="••••••••"
                />
                <button
                  type="button"
                  onClick={() => setShow((v) => !v)}
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  aria-label={show ? t('login.hide') : t('login.show')}
                >
                  {show ? <EyeSlash size={16} /> : <Eye size={16} />}
                </button>
              </div>
            </div>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
            <Button type="submit" className="w-full" disabled={!password || busy}>
              {busy ? t('login.signingIn') : t('login.signIn')}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
