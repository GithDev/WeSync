import { Laptop, FolderPlus, Folder } from 'lucide-react';
import { BaseModal } from '../base/Modal/Modal';
import { Button } from '../base/Button/Button';

/**
 * Getting-started guide — triangle layout so step 3 reads as the result of
 * combining steps 1 and 2:
 *   ① Trust your devices    ② Add a folder
 *           ③ Share folder with device
 *
 * Auto-opens on first /devices visit for users without trusted devices;
 * re-openable via help button.
 */

interface StepProps {
  n: number;
  illustration: React.ReactNode;
  tint: string;
  badge: string;
  label: string;
  description: string;
}

function Step({ n, illustration, tint, badge, label, description }: StepProps) {
  return (
    <div className="flex flex-col items-center gap-2 max-w-[140px] text-center">
      <div className="relative">
        <div className={`w-20 h-20 rounded-full border-2 flex items-center justify-center ${tint}`}>
          {illustration}
        </div>
        <span
          className={`absolute -top-1 -right-1 w-6 h-6 rounded-full text-white text-xs font-bold flex items-center justify-center ring-2 ring-white ${badge}`}
        >
          {n}
        </span>
      </div>
      <p className="text-xs font-semibold text-slate-700 leading-tight">{label}</p>
      <p className="text-[10px] text-slate-400 leading-snug">{description}</p>
    </div>
  );
}

interface Props {
  open: boolean;
  onClose: () => void;
}

export function GettingStarted({ open, onClose }: Props) {
  return (
    <BaseModal open={open} title="Get started with WeSync" maxWidth="max-w-md">
      <div className="flex flex-col items-center gap-6 py-2 select-none">
        {/* Row 1: Trust + Folder */}
        <div className="flex items-start justify-center gap-8">
          <Step
            n={1}
            tint="bg-blue-50 border-blue-100 text-blue-500"
            badge="bg-blue-500"
            label="Trust your devices"
            description="Make your device discoverable and trust the ones you own"
            illustration={<Laptop className="w-10 h-10" strokeWidth={1.75} />}
          />
          <Step
            n={2}
            tint="bg-amber-50 border-amber-100 text-amber-500"
            badge="bg-amber-500"
            label="Add a folder"
            description="Pick a folder on this device you want to keep in sync"
            illustration={<FolderPlus className="w-10 h-10" strokeWidth={1.75} />}
          />
        </div>

        {/* Row 2: Share — centered below */}
        <div className="flex justify-center">
          <Step
            n={3}
            tint="bg-emerald-50 border-emerald-100 text-emerald-500"
            badge="bg-emerald-500"
            label="Share folder with device"
            description="Invite a trusted device to sync the folder with you"
            illustration={
              <div className="flex items-center gap-1.5">
                <Laptop className="w-7 h-7" strokeWidth={1.75} />
                <Folder className="w-7 h-7" strokeWidth={1.75} />
              </div>
            }
          />
        </div>
      </div>

      <div className="flex justify-end">
        <Button variant="primary" onClick={onClose} className="sm:w-auto w-full">
          Got it
        </Button>
      </div>
    </BaseModal>
  );
}
