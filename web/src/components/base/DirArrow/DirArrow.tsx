import { FolderDirection } from '../../../types/enums';

const CONFIG: Record<FolderDirection, { arrow: string; label: string }> = {
  [FolderDirection.SendReceive]: { arrow: '←→', label: 'Two-way' },
  [FolderDirection.SendOnly]: { arrow: '→', label: 'Send only' },
  [FolderDirection.ReceiveOnly]: { arrow: '←', label: 'Receive only' },
};

export function dirLabel(type: string): string {
  return CONFIG[type as FolderDirection]?.label ?? 'Two-way';
}

interface Props {
  type: string;
  className?: string;
}

export function DirArrow({ type, className = '' }: Props) {
  const { arrow } = CONFIG[type as FolderDirection] ?? CONFIG[FolderDirection.SendReceive];
  return (
    <span className={`font-mono inline-block w-7 text-center flex-shrink-0 ${className}`}>
      {arrow}
    </span>
  );
}
