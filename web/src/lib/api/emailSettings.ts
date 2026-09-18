import { apiFetch } from './client'

export type EmailSettings = {
  smtpHost: string
  smtpPort: number
  smtpUsername?: string
  // The SMTP password is never echoed back by the API (see
  // internal/api.emailSettingsDTO's own doc comment) — passwordSet is
  // all the console ever learns about whether one is stored.
  passwordSet: boolean
  useTls: boolean
  skipVerify: boolean
  fromAddress: string
  fromName?: string
  configured: boolean
}

// All fields optional and nil-means-"leave unchanged" on the wire — see
// internal/api.updateEmailSettingsRequest's own doc comment. An explicit
// empty string for smtpPassword clears it; omitting the field entirely
// (undefined, dropped by JSON.stringify) leaves the stored one untouched.
export type UpdateEmailSettingsRequest = Partial<{
  smtpHost: string
  smtpPort: number
  smtpUsername: string
  smtpPassword: string
  useTls: boolean
  skipVerify: boolean
  fromAddress: string
  fromName: string
}>

export const emailSettingsApi = {
  get: () => apiFetch<EmailSettings>('/system/email'),
  update: (body: UpdateEmailSettingsRequest) =>
    apiFetch<EmailSettings>('/system/email', { method: 'PATCH', body: JSON.stringify(body) }),
  test: (to: string) => apiFetch<{ sent: boolean }>('/system/email/test', { method: 'POST', body: JSON.stringify({ to }) }),
}
