import { useState } from 'react';

// One canonical set of ignore presets, shared by the add-folder modal and the
// folder settings page so both offer the same templates.
export const IGNORE_TEMPLATES: { label: string; patterns: string[] }[] = [
  { label: 'Node.js', patterns: ['node_modules/', '.npm/', 'dist/', '.next/'] },
  { label: 'Python', patterns: ['.venv/', '__pycache__/', '*.pyc', '.pytest_cache/'] },
  { label: 'Git', patterns: ['.git/'] },
  { label: 'macOS', patterns: ['.DS_Store', '.Spotlight-V100', '.Trashes'] },
  { label: 'Build output', patterns: ['build/', 'dist/', 'out/', '.cache/'] },
  { label: '.env files', patterns: ['.env', '.env.*', '*.env'] },
];

interface Props {
  patterns: string[];
  /**
   * Called with the full next list whenever a pattern is added, removed or a
   * template applied. The parent owns persistence — the add-folder flow keeps
   * it in local state, the settings page writes it straight to the backend.
   */
  onChange: (next: string[]) => void;
}

/** Glob ignore-pattern editor: quick templates, current pattern chips, and a
 *  free-text add field. Controlled — holds only its own draft input. */
export function IgnorePatternsEditor({ patterns, onChange }: Props) {
  const [draft, setDraft] = useState('');

  const add = () => {
    const trimmed = draft.trim();
    if (!trimmed || patterns.includes(trimmed)) return;
    onChange([...patterns, trimmed]);
    setDraft('');
  };

  const applyTemplate = (preset: string[]) =>
    onChange(Array.from(new Set([...patterns, ...preset])));
  const remove = (pattern: string) => onChange(patterns.filter((p) => p !== pattern));

  return (
    <div>
      {/* Quick templates */}
      <div className="flex flex-wrap gap-1.5 mb-3">
        {IGNORE_TEMPLATES.map((t) => (
          <button
            key={t.label}
            type="button"
            onClick={() => applyTemplate(t.patterns)}
            className="text-xs px-2 py-1 rounded-lg border border-slate-200 text-slate-600 hover:bg-slate-100 transition-colors"
          >
            {t.label}
          </button>
        ))}
      </div>

      {/* Current patterns */}
      {patterns.length > 0 && (
        <div className="flex flex-wrap gap-2 mb-3">
          {patterns.map((p) => (
            <span
              key={p}
              className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full bg-slate-100 border border-slate-200 text-xs font-mono text-slate-700"
            >
              {p}
              <button
                type="button"
                onClick={() => remove(p)}
                className="text-slate-400 hover:text-red-500 transition-colors leading-none ml-0.5"
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}

      {/* Add pattern */}
      <div className="flex gap-2">
        <input
          type="text"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && add()}
          placeholder="e.g. *.tmp or node_modules/"
          className="flex-1 text-sm px-3 py-1.5 rounded-xl border border-slate-200 focus:outline-none focus:border-blue-400 font-mono placeholder:font-sans placeholder:text-slate-400"
        />
        <button
          type="button"
          onClick={add}
          disabled={!draft.trim()}
          className="px-3 py-1.5 rounded-xl text-sm bg-slate-100 text-slate-600 hover:bg-slate-200 disabled:opacity-40 transition-colors"
        >
          Add
        </button>
      </div>
    </div>
  );
}
