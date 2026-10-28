import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, type AutonomyBacktest, type RiskFlagCode, type Workflow, type WorkflowLabel } from '../../api/client'
import {
  formDiffers,
  formFromLabel,
  gateHasDiff,
  toLabelAutonomy,
  withGateAutonomy,
  withMode,
  type DialForm,
  type DialMode,
} from '../../lib/autonomyDial'
import { useWorkflowStore } from '../../stores/workflow'

const DEBOUNCE_MS = 400

/** Form state, debounced backtest and save for one gate; shared by the panel and the phone sheet. */
export function useGateDial(workflow: Workflow, label: WorkflowLabel, onSaved?: () => void) {
  const fetchWorkflows = useWorkflowStore((s) => s.fetch)
  const [form, setForm] = useState<DialForm>(() => formFromLabel(label))
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [stale, setStale] = useState(false)
  const loadedRef = useRef(JSON.stringify(label.autonomy ?? null))

  // Re-initialise when the gate or the saved workflow changes.
  useEffect(() => {
    setForm(formFromLabel(label))
    loadedRef.current = JSON.stringify(label.autonomy ?? null)
    setStale(false)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [label.name, workflow.id, workflow.updated_at])

  const hasDiff = useMemo(() => gateHasDiff(workflow, label.name), [workflow, label.name])
  const differs = formDiffers(form, label)

  const setMode = useCallback((mode: DialMode) => { setSaved(false); setForm((f) => withMode(f, mode)) }, [])
  const toggleFlag = useCallback((code: RiskFlagCode) => {
    setSaved(false)
    setForm((f) => ({
      ...f,
      ask_on: f.ask_on.includes(code) ? f.ask_on.filter((c) => c !== code) : [...f.ask_on, code],
    }))
  }, [])
  const patch = useCallback((p: Partial<DialForm>) => { setSaved(false); setForm((f) => ({ ...f, ...p })) }, [])
  const reset = useCallback(() => { setForm(formFromLabel(label)); setSaveError(null); setSaved(false) }, [label])

  // ── Backtest ──
  const [backtest, setBacktest] = useState<AutonomyBacktest | null>(null)
  const [btLoading, setBtLoading] = useState(false)
  const [btError, setBtError] = useState<string | null>(null)
  const seq = useRef(0)
  const askKey = form.ask_on.join(',')
  useEffect(() => {
    const mine = ++seq.current
    const ctl = new AbortController()
    setBtLoading(true)
    const timer = setTimeout(() => {
      api.autonomy
        .backtest(
          workflow.id,
          label.name,
          { mode: form.mode, ask_on: form.mode === 'on_flags' ? form.ask_on : [], max_cost_usd: form.max_cost_usd, days: 30 },
          ctl.signal,
        )
        .then((r) => {
          if (mine !== seq.current) return
          setBacktest(r)
          setBtError(null)
          setBtLoading(false)
        })
        .catch((e) => {
          if (mine !== seq.current || ctl.signal.aborted) return
          setBtError(e instanceof Error ? e.message : 'Backtest failed')
          setBtLoading(false)
        })
    }, DEBOUNCE_MS)
    return () => {
      clearTimeout(timer)
      ctl.abort()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workflow.id, label.name, form.mode, askKey, form.max_cost_usd])

  // ── Save ──
  const saveForm = useCallback(
    async (next: DialForm) => {
      setSaving(true)
      setSaveError(null)
      setSaved(false)
      try {
        const fresh = await api.workflows.get(workflow.id)
        const freshLabel = fresh.labels.find((l) => l.name === label.name)
        if (!freshLabel) throw new Error(`Label ${label.name} no longer exists`)
        if (JSON.stringify(freshLabel.autonomy ?? null) !== loadedRef.current) {
          setStale(true)
          return false
        }
        await api.workflows.updateYaml(workflow.id, withGateAutonomy(fresh, label.name, toLabelAutonomy(next)))
        await fetchWorkflows()
        setSaved(true)
        onSaved?.()
        return true
      } catch (e) {
        setSaveError(e instanceof Error ? e.message : 'Save failed')
        return false
      } finally {
        setSaving(false)
      }
    },
    [workflow.id, label.name, fetchWorkflows, onSaved],
  )
  const save = useCallback(() => saveForm(form), [saveForm, form])
  const reload = useCallback(async () => { await fetchWorkflows(); setStale(false) }, [fetchWorkflows])

  return {
    form, setMode, toggleFlag, patch, reset, differs, hasDiff,
    backtest, btLoading, btError,
    save, saveForm, saving, saveError, saved, stale, reload,
  }
}

export type GateDial = ReturnType<typeof useGateDial>
