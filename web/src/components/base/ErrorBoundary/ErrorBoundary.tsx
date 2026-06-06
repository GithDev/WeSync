import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';

interface Props {
  children: ReactNode;
  /** Shown in the heading so the user knows what failed (e.g. "this page"). */
  scope: string;
}

interface State {
  error: Error | null;
  info: ErrorInfo | null;
}

/**
 * Catches render-time exceptions so a single bad component can't blank the
 * whole app (the cause of the "white screen" crash). Without a boundary, any
 * thrown error during render unmounts the entire React tree and leaves an
 * empty DOM. This shows a recoverable message instead — and surfaces the
 * actual error + stack so the underlying bug can be pinned down.
 *
 * Must be a class component — React only supports error boundaries via the
 * lifecycle methods below, there's no hook equivalent.
 */
export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { error: null, info: null };
  }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // Keep the real error in the console even though we render a fallback —
    // this is what lets us trace the root cause.

    console.error('[ErrorBoundary]', error, info.componentStack);
    this.setState({ info });
  }

  private reset = () => this.setState({ error: null, info: null });

  render(): ReactNode {
    const { error, info } = this.state;
    const { children, scope } = this.props;
    if (!error) return children;

    const heading = `Something went wrong loading ${scope}`;
    const details = `${error.message}${info?.componentStack ?? ''}`;

    return (
      <div className="min-h-[60vh] flex flex-col items-center justify-center gap-4 px-6 py-10 text-center">
        <div className="w-12 h-12 rounded-full bg-red-50 border border-red-100 flex items-center justify-center">
          <span className="text-red-400 text-2xl leading-none">⚠</span>
        </div>
        <div>
          <p className="text-sm font-semibold text-slate-700">{heading}</p>
          <p className="text-xs text-slate-400 mt-1 max-w-sm">
            The view hit an unexpected error. Your data is safe — try again, or reload the app.
          </p>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={this.reset}
            className="px-4 py-1.5 rounded-xl text-sm bg-slate-100 text-slate-600 hover:bg-slate-200 transition-colors"
          >
            Try again
          </button>
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="px-4 py-1.5 rounded-xl text-sm bg-blue-500 text-white hover:bg-blue-600 transition-colors"
          >
            Reload
          </button>
        </div>
        {/* Collapsed by default — expandable details for diagnosis. */}
        <details className="mt-2 max-w-lg w-full text-left">
          <summary className="text-xs text-slate-400 cursor-pointer hover:text-slate-600">
            Error details
          </summary>
          <pre className="mt-2 text-[10px] font-mono text-slate-500 bg-slate-50 border border-slate-200 rounded-lg p-3 overflow-auto max-h-48 whitespace-pre-wrap">
            {details}
          </pre>
        </details>
      </div>
    );
  }
}
