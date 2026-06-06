import * as AlertDialog from '@radix-ui/react-alert-dialog';
import { Button } from '../Button/Button';

// ── BaseModal ────────────────────────────────────────────────────────────────
// Shared overlay + content shell used by all modal dialogs.

interface BaseModalProps {
  open: boolean;
  title: string;
  description?: string;
  maxWidth?: string;
  children: React.ReactNode;
}

export function BaseModal({
  open,
  title,
  description,
  maxWidth = 'max-w-sm',
  children,
}: BaseModalProps) {
  return (
    <AlertDialog.Root open={open}>
      <AlertDialog.Portal>
        <AlertDialog.Overlay className="fixed inset-0 bg-black/25 backdrop-blur-sm z-40 animate-fade-in" />
        <AlertDialog.Content
          className={`fixed left-4 right-4 bottom-4 sm:left-1/2 sm:right-auto sm:bottom-auto sm:top-1/2 sm:-translate-x-1/2 sm:-translate-y-1/2 z-50 bg-white rounded-2xl shadow-xl px-5 py-5 sm:px-6 sm:w-full ${maxWidth} animate-fade-in flex flex-col gap-4`}
        >
          <div>
            <AlertDialog.Title className="text-base font-semibold text-slate-900">
              {title}
            </AlertDialog.Title>
            {description && (
              <AlertDialog.Description className="text-sm text-slate-500 mt-1">
                {description}
              </AlertDialog.Description>
            )}
          </div>
          {children}
        </AlertDialog.Content>
      </AlertDialog.Portal>
    </AlertDialog.Root>
  );
}

// ── ModalFooter ───────────────────────────────────────────────────────────────

interface ModalFooterProps {
  confirmLabel?: string;
  cancelLabel?: string;
  confirmVariant?: 'primary' | 'danger';
  confirmDisabled?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ModalFooter({
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  confirmVariant = 'primary',
  confirmDisabled = false,
  onConfirm,
  onCancel,
}: ModalFooterProps) {
  return (
    <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
      <AlertDialog.Cancel asChild>
        <Button variant="secondary" outlined onClick={onCancel} className="sm:w-auto w-full">
          {cancelLabel}
        </Button>
      </AlertDialog.Cancel>
      <AlertDialog.Action asChild>
        <Button
          variant={confirmVariant}
          onClick={onConfirm}
          disabled={confirmDisabled}
          className="sm:w-auto w-full"
        >
          {confirmLabel}
        </Button>
      </AlertDialog.Action>
    </div>
  );
}
