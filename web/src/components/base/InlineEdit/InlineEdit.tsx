import { useState, useRef, useEffect } from 'react';

interface Props {
  value: string;
  onSave: (value: string) => Promise<void> | void;
  className?: string;
  inputClassName?: string;
  placeholder?: string;
  showPencil?: boolean;
}

export function InlineEdit({
  value,
  onSave,
  className = '',
  inputClassName = '',
  placeholder,
  showPencil = false,
}: Props) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editing) inputRef.current?.select();
  }, [editing]);

  const start = () => {
    setDraft(value);
    setEditing(true);
  };

  const commit = async () => {
    const trimmed = draft.trim();
    if (trimmed && trimmed !== value) await onSave(trimmed);
    setEditing(false);
  };

  const cancel = () => setEditing(false);

  if (editing) {
    return (
      <form
        onSubmit={(e) => {
          e.preventDefault();
          commit();
        }}
        className="contents"
      >
        <input
          ref={inputRef}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === 'Escape') cancel();
          }}
          placeholder={placeholder}
          className={`border-b-2 border-blue-400 outline-none bg-transparent ${inputClassName}`}
        />
      </form>
    );
  }

  return (
    <span className="flex items-center gap-1.5 group">
      <span className={className}>
        {value || <span className="opacity-40">{placeholder}</span>}
      </span>
      <button
        type="button"
        onClick={start}
        title="Rename"
        className={`transition-opacity text-slate-400 hover:text-slate-600 ${showPencil ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'}`}
      >
        <svg
          className="w-3.5 h-3.5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
        >
          <path
            d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          <path
            d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>
    </span>
  );
}
