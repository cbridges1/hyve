import { useState } from 'react'
import { auth } from '../lib/api'
import { useAction } from '../lib/hooks'
import { TextField } from './form'
import { Modal } from './Modal'

export function ChangePassword({ username, onClose }: { username: string; onClose: () => void }) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [done, setDone] = useState(false)
  const { busy, error, run } = useAction()

  return (
    <Modal title="Change password" onClose={onClose}>
      {done ? (
        <>
          <p className="mb-4 text-sm text-neutral-600 dark:text-neutral-400">Your password has been changed. Your other sessions were signed out.</p>
          <div className="flex justify-end">
            <button type="button" className="btn btn-primary" onClick={onClose}>
              Done
            </button>
          </div>
        </>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            run(async () => {
              await auth.password(username, current, next)
              setDone(true)
            })
          }}
        >
          <div className="mb-3 space-y-3">
            <TextField label="Current password" type="password" value={current} onChange={setCurrent} autoComplete="current-password" autoFocus />
            <TextField label="New password" type="password" value={next} onChange={setNext} autoComplete="new-password" />
          </div>
          {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
          <div className="flex justify-end gap-2">
            <button type="button" className="btn btn-tertiary" onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className="btn btn-primary" disabled={!current || !next || busy}>
              {busy ? 'Changing…' : 'Change password'}
            </button>
          </div>
        </form>
      )}
    </Modal>
  )
}
