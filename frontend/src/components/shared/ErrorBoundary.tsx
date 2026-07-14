import { Component, type ErrorInfo, type ReactNode } from 'react'

// ErrorBoundary catches render/lifecycle errors in its subtree and shows the
// error on screen instead of unmounting to a blank page. Without this, any
// thrown error in a page component blanks the whole app (React unmounts the
// tree). Class component because error boundaries have no hook equivalent.
export default class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Log so it also shows in the console with the component stack.
    console.error('ErrorBoundary caught:', error, info.componentStack)
  }

  render() {
    if (this.state.error) {
      return (
        <div className="p-6 text-sm text-slate-200 font-mono">
          <div className="text-red-400 font-bold mb-2">Something crashed rendering this page.</div>
          <div className="mb-3 text-red-300">{this.state.error.message}</div>
          <pre className="whitespace-pre-wrap text-xs text-slate-400 bg-slate-900 p-3 rounded overflow-auto max-h-[60vh]">
            {this.state.error.stack}
          </pre>
          <button
            onClick={() => this.setState({ error: null })}
            className="mt-3 px-3 py-1 rounded bg-slate-700 hover:bg-slate-600 text-xs"
          >
            Retry
          </button>
        </div>
      )
    }
    return this.props.children
  }
}
