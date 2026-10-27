import ModalShell from '../shared/ModalShell'
import LessonForm, { type LessonFormValues } from './LessonForm'
import { api, type RepoLesson } from '../../api/client'
import { LESSON_BODY_MAX, lessonPrefill } from '../../lib/lessons'

export default function RememberLessonModal({
  repoId,
  repoName,
  taskId,
  text,
  onClose,
  onSaved,
}: {
  repoId: string
  repoName?: string
  taskId: string
  text: string
  onClose: () => void
  onSaved: (lesson: RepoLesson) => void
}) {
  const trimmed = text.trim().length > LESSON_BODY_MAX

  const submit = async (values: LessonFormValues) => {
    const lesson = await api.repos.createLesson(repoId, {
      ...values,
      status: 'active',
      source: 'human',
      source_task_id: taskId,
    })
    onSaved(lesson)
  }

  return (
    <ModalShell onClose={onClose} ariaLabel="Remember for this repo" className="w-full max-w-lg mx-4 bg-slate-900 border border-slate-700 rounded-xl p-4 sm:p-5">
      <h2 className="text-base font-semibold text-slate-100 mb-1">Remember for this repo</h2>
      <p className="text-xs text-slate-500 mb-3 break-words">
        Saved as an active lesson{repoName ? ` for ${repoName}` : ''} and shown to future agent runs.
        {trimmed && ' Trimmed to 300 characters.'}
      </p>
      <LessonForm
        initial={{ kind: 'preference', body: lessonPrefill(text) }}
        submitLabel="Save lesson"
        onSubmit={submit}
        onCancel={onClose}
        autoFocus
      />
    </ModalShell>
  )
}
