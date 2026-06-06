export type Direction = 'sendreceive' | 'sendonly' | 'receiveonly';

const CONFIG: Record<Direction, { arrow: string; label: string }> = {
  sendreceive: { arrow: '←→', label: 'Two-way' },
  sendonly: { arrow: '→', label: 'Send only' },
  receiveonly: { arrow: '←', label: 'Receive only' },
};

export function dirLabel(type: string): string {
  return CONFIG[type as Direction]?.label ?? 'Two-way';
}

interface Props {
  type: string;
  className?: string;
}

export function DirArrow({ type, className = '' }: Props) {
  const { arrow } = CONFIG[type as Direction] ?? CONFIG.sendreceive;
  return (
    <span className={`font-mono inline-block w-7 text-center flex-shrink-0 ${className}`}>
      {arrow}
    </span>
  );
}
