/** Same rule as the API's secretKeyPattern (internal/api/secrets.go). */
export const SECRET_KEY_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/

export interface DotenvEntry {
  key: string
  value: string
}

export interface DotenvResult {
  entries: DotenvEntry[]
  /** 1-based line numbers (and text) that couldn't be parsed. */
  errors: { line: number; text: string }[]
}

/**
 * Parses .env-style text: KEY=VALUE lines, optional `export ` prefix, `#`
 * comments, and single- or double-quoted values (which may span lines;
 * double quotes also understand \n, \t, \" and \\). A later duplicate key
 * wins, as in a shell.
 */
export function parseDotenv(text: string): DotenvResult {
  const lines = text.replace(/\r\n?/g, '\n').split('\n')
  const byKey = new Map<string, string>()
  const errors: DotenvResult['errors'] = []

  for (let i = 0; i < lines.length; i++) {
    const startLine = i + 1
    const trimmed = lines[i].trim()
    if (trimmed === '' || trimmed.startsWith('#')) continue

    const m = /^(?:export\s+)?([^=\s]+)\s*=\s*(.*)$/.exec(trimmed)
    if (!m || !SECRET_KEY_PATTERN.test(m[1])) {
      errors.push({ line: startLine, text: lines[i] })
      continue
    }
    const key = m[1]
    let rest = m[2]

    const quote = rest[0]
    if (quote === '"' || quote === "'") {
      // Gather lines until the closing, unescaped quote.
      let body = rest.slice(1)
      let close = findClosingQuote(body, quote)
      while (close < 0 && i + 1 < lines.length) {
        i++
        body += '\n' + lines[i]
        close = findClosingQuote(body, quote)
      }
      if (close < 0) {
        errors.push({ line: startLine, text: lines[startLine - 1] + ' (unterminated quote)' })
        i = startLine - 1 // resume on the next line rather than swallowing the rest
        continue
      }
      const raw = body.slice(0, close)
      byKey.set(key, quote === '"' ? unescapeDouble(raw) : raw)
      continue
    }

    // Unquoted: an inline comment needs whitespace before the '#'.
    rest = rest.replace(/\s+#.*$/, '').trim()
    byKey.set(key, rest)
  }

  return { entries: [...byKey].map(([key, value]) => ({ key, value })), errors }
}

function findClosingQuote(s: string, quote: string): number {
  for (let i = 0; i < s.length; i++) {
    if (quote === '"' && s[i] === '\\') {
      i++
      continue
    }
    if (s[i] === quote) return i
  }
  return -1
}

function unescapeDouble(s: string): string {
  return s.replace(/\\([nrt"\\])/g, (_, c: string) => ({ n: '\n', r: '\r', t: '\t', '"': '"', '\\': '\\' })[c] ?? c)
}

/** Turns a file name into a plausible key: "my-kubeconfig.yaml" -> "MY_KUBECONFIG". */
export function keyFromFileName(name: string): string {
  const stem = name.replace(/\.[^.]*$/, '')
  let key = stem.replace(/[^A-Za-z0-9_]+/g, '_').replace(/^_+|_+$/g, '').toUpperCase()
  if (/^[0-9]/.test(key)) key = '_' + key
  return key
}
