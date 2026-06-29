import { useEffect, useState } from 'react'
import { api } from '../../api/client'

export default function GitHubAuthWarning() {
  const [authed, setAuthed] = useState<boolean | null>(null)

  useEffect(() => {
    api.github.authStatus()
      .then((s) => setAuthed(s.authed))
      .catch(() => setAuthed(false))
  }, [])

  if (authed === null || authed === true) return null

  return (
    <div className="bg-yellow-900/40 border border-yellow-700 text-yellow-200 text-xs px-3 py-2 rounded flex items-center gap-2">
      <span>⚠</span>
      <span>
        GitHub credentials not found — PR state sync is unavailable.
        Mount <code className="bg-yellow-900/60 px-1 rounded">~/.config/gh</code> as a Docker
        volume or set the <code className="bg-yellow-900/60 px-1 rounded">GITHUB_TOKEN</code> env
        var to enable it.
      </span>
    </div>
  )
}
