import { Pill } from '../Pill/Pill';
import { DirArrow } from '../DirArrow/DirArrow';
import type { FolderRelationState } from '../../../api/client';
import { folderDeviceDisplay, dotColorClass } from '../../../state/folder-display';

// DevicePill renders a peer device in three visual modes (display /
// removable / selectable). The folder-relation `state` is the single
// driver for badges and dot colors when it's present; without it
// (pickers, offerer lists, anywhere not inside a folder context) we
// fall back to the bare `connected` boolean.
//
// All UI mapping for the FolderRelationState lives in folder-display.ts.
// This component never reads the raw state string.

interface BaseProps {
  name: string;
  hostname?: string;
  /** Device-level peerwire connection — drives the dot only when `state` is absent. */
  connected: boolean;
  /**
   * Folder-relation state for this (folder, device) pair. Present in
   * folder-context renders; undefined for non-folder contexts (pickers,
   * incoming-folder offerer lists). Drives every visual when present.
   */
  state?: FolderRelationState;
  directionType?: string;
  fullWidth?: boolean;
}

type DisplayProps = BaseProps & { mode: 'display' };
type RemovableProps = BaseProps & { mode: 'removable'; onRemove: () => void; title?: string };
type SelectableProps = BaseProps & { mode: 'selectable'; selected: boolean; onToggle: () => void };
type Props = DisplayProps | RemovableProps | SelectableProps;

function StatusDot({
  state,
  connected,
  removable = false,
}: {
  state: FolderRelationState | undefined;
  connected: boolean;
  removable?: boolean;
}) {
  // With state → trust the central display map. Without → bare connection.
  // The hover-red treatment (removable mode) is purely visual, stacked over either.
  const colorClass = state
    ? dotColorClass(folderDeviceDisplay(state).dotColor)
    : connected
      ? 'bg-emerald-400'
      : 'bg-slate-300';
  return (
    <span
      className={`w-1.5 h-1.5 rounded-full flex-shrink-0 transition-colors ${removable ? 'group-hover:bg-red-400' : ''} ${colorClass}`}
    />
  );
}

function Label({
  name,
  hostname,
  fullWidth,
}: {
  name: string;
  hostname?: string;
  fullWidth?: boolean;
}) {
  return (
    <>
      <span className={`leading-none ${fullWidth ? 'flex-1 truncate' : ''}`}>{name}</span>
      {hostname && hostname !== name && (
        <span className={`opacity-60 leading-none ${fullWidth ? '' : 'text-[0.625rem]'}`}>
          {hostname}
        </span>
      )}
    </>
  );
}

// Secondary inline content after the name. Order of preference:
//   1. "Invited" badge when pending
//   2. State-specific label ("Syncing", "Paused", "Paused by remote")
//   3. Direction arrow when there is one
//   4. Nothing
function SecondaryAffix({
  state,
  directionType,
}: {
  state: FolderRelationState | undefined;
  directionType?: string;
}) {
  const display = state ? folderDeviceDisplay(state) : undefined;
  if (display?.pending) {
    return <span className="text-[10px] leading-none opacity-60 italic">Invited</span>;
  }
  if (display?.label) {
    return <span className="text-[10px] leading-none opacity-60 italic">{display.label}</span>;
  }
  if (directionType) {
    return <DirArrow type={directionType} className="text-[10px] opacity-50" />;
  }
  return null;
}

export function DevicePill(props: Props) {
  const { name, hostname, connected, state, directionType, fullWidth } = props;

  const display = state ? folderDeviceDisplay(state) : undefined;

  if (props.mode === 'selectable') {
    // Picker context — no folder relation, no badges, just selectable affordance.
    const { selected, onToggle } = props;
    return (
      <Pill variant={selected ? 'selected' : 'selectable'} fullWidth={fullWidth} onClick={onToggle}>
        <StatusDot state={undefined} connected={connected} />
        <Label name={name} hostname={hostname} fullWidth={fullWidth} />
        <span
          className={`w-4 h-4 rounded-full border-2 flex-shrink-0 ml-auto transition-colors ${selected ? 'border-emerald-500 bg-emerald-500' : 'border-slate-300'}`}
        />
      </Pill>
    );
  }

  if (props.mode === 'display') {
    return (
      <Pill variant={display?.pending ? 'pending' : 'default'} fullWidth={fullWidth}>
        <StatusDot state={state} connected={connected} />
        <Label name={name} hostname={hostname} fullWidth={fullWidth} />
        <SecondaryAffix state={state} directionType={directionType} />
      </Pill>
    );
  }

  // removable — pill variant chosen from the central display map so the
  // background tone matches the dot color we just rendered.
  const variant = display?.pending
    ? 'pending'
    : display?.dotColor === 'emerald'
      ? 'connected'
      : 'offline';
  return (
    <Pill variant={variant} fullWidth={fullWidth} onClick={props.onRemove} title={props.title}>
      <StatusDot state={state} connected={connected} removable />
      <Label name={name} hostname={hostname} fullWidth={fullWidth} />
      <SecondaryAffix state={state} directionType={directionType} />
      <span className="opacity-0 group-hover:opacity-100 leading-none transition-opacity">×</span>
    </Pill>
  );
}
