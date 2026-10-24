// Keeps one broken page from blanking the whole app: shows what went
// wrong, with a way back.
import { Component, type ReactNode } from 'react'

export class ErrorBoundary extends Component<{ children: ReactNode; resetKey?: string }, { error: Error | null }> {
  state = { error: null as Error | null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  componentDidUpdate(prev: { resetKey?: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null })
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <div className="banner banner-error">
        <p className="mb-2 font-medium">This page hit an error.</p>
        <pre className="mb-2 font-mono text-xs whitespace-pre-wrap">{this.state.error.message}</pre>
        <button type="button" className="btn btn-secondary btn-sm" onClick={() => this.setState({ error: null })}>
          Try again
        </button>
      </div>
    )
  }
}
