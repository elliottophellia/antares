import { useState } from 'react'
import { ArrowSquareOut, CheckCircle, Key } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { PageBody } from '@/components/layout/AppShell'
import { Button } from '@/components/ui/button'
import { Badge, Card, Input } from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'

interface KeySpec {
  id: string
  label: string
  description: string
  howto: string
  howto_url: string
  optional: boolean
  set: boolean
}

export default function KeysPage() {
  const { data, loading, reload } = useApi<{ keys: KeySpec[] }>('/osint/keys')
  const [values, setValues] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState<string | null>(null)

  const save = async (id: string, value: string) => {
    setSaving(id)
    try {
      await post('/osint/keys', { id, value })
      setValues((v) => ({ ...v, [id]: '' }))
      reload()
    } finally {
      setSaving(null)
    }
  }

  return (
    <PageBody>
      <div className="mb-4 flex items-center gap-3">
        <Key className="size-5 text-primary" />
        <div>
          <h1 className="text-lg font-semibold">OSINT API keys</h1>
          <p className="text-sm text-muted-foreground">
            Optional keys that unlock the paid/registered OSINT lookups. Stored locally in antares. The keyless
            tools (DNS, WHOIS, usernames, GitHub, crypto, …) work without any of these.
          </p>
        </div>
      </div>

      {loading ? (
        <SkeletonList count={5} />
      ) : (
        <div className="space-y-3">
          {(data?.keys ?? []).map((k) => (
            <Card key={k.id} className="p-4">
              <div className="mb-1 flex items-center gap-2">
                <span className="font-medium">{k.label}</span>
                {k.optional ? <Badge variant="outline">optional</Badge> : null}
                {k.set ? (
                  <Badge variant="success" className="gap-1">
                    <CheckCircle className="size-3" weight="fill" /> configured
                  </Badge>
                ) : (
                  <Badge variant="secondary">not set</Badge>
                )}
              </div>
              <p className="text-sm text-muted-foreground">{k.description}</p>
              <p className="mt-1 text-[13px] text-muted-foreground">
                <span className="text-foreground/70">How to get it:</span> {k.howto}{' '}
                <a
                  href={k.howto_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-0.5 text-primary hover:underline"
                >
                  Open <ArrowSquareOut className="size-3" />
                </a>
              </p>
              <div className="mt-2 flex gap-2">
                <Input
                  type="password"
                  value={values[k.id] ?? ''}
                  onChange={(e) => setValues((v) => ({ ...v, [k.id]: e.target.value }))}
                  placeholder={k.set ? '•••••••• (replace)' : `paste your ${k.label} key`}
                  className="font-mono"
                />
                <Button size="sm" disabled={saving === k.id} onClick={() => save(k.id, values[k.id] ?? '')}>
                  {saving === k.id ? 'Saving…' : 'Save'}
                </Button>
                {k.set ? (
                  <Button variant="outline" size="sm" onClick={() => save(k.id, '')}>
                    Clear
                  </Button>
                ) : null}
              </div>
            </Card>
          ))}
        </div>
      )}
    </PageBody>
  )
}
