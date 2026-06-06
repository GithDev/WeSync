import { useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { Breadcrumbs } from '../../components/base/Breadcrumbs/Breadcrumbs';
import { api } from '../../api/client';
import { useWS } from '../../api/websocket';
import { useToast } from '../../components/base/Toast/Toast';
import { Button } from '../../components/base/Button/Button';
import { AsyncButton } from '../../components/base/Button/AsyncButton';
import { useConfirm } from '../../components/base/ConfirmDialog/ConfirmDialog';
import { FolderIcon } from '../../components/base/FolderIcon/FolderIcon';
import { Card } from '../../components/base/Card/Card';
import { useApiToast } from '../../hooks/useApiToast';

const OS_LABEL: Record<string, string> = {
  windows: 'Windows',
  linux: 'Linux',
  darwin: 'macOS',
};

export function DevicePage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { addToast } = useToast();
  const run = useApiToast();
  const { devices, folders, visible } = useWS();

  const device = devices.find((d) => d.deviceID === id);
  const info = device?.info;

  const name = info?.hostname ?? device?.name ?? id!.slice(0, 7);
  const sharedFolderCount = folders.filter((f) => f.deviceIDs?.includes(id!)).length;

  const removeTrustDescription =
    sharedFolderCount > 0
      ? `${name} will be removed from ${sharedFolderCount} shared folder${sharedFolderCount > 1 ? 's' : ''}. Files are kept on both devices.`
      : `${name} will no longer be reachable via WeSync. Files are kept on both devices.`;

  // useConfirm is a hook, so it must run on every render — keep it (and every
  // other hook) above the early return below, or hook order breaks when the
  // device disappears mid-view.
  const handleRemoveTrust = useConfirm(
    async () => {
      await run(api.removeDevice(id!), 'Could not remove device');
      navigate('/devices');
    },
    { title: `Remove ${name}?`, description: removeTrustDescription, confirmLabel: 'Remove' },
  );

  const handleCancelRequest = async () => {
    await run(api.removeDevice(id!), 'Could not cancel request');
    navigate('/devices');
  };

  // The device may have been removed (here or from another device) while this
  // page was open, or the URL may be a stale link. Once the first WS snapshot
  // has arrived (visible !== null), a missing device means it's genuinely gone:
  // send the user home with a heads-up rather than leaving a dead page.
  const wsReady = visible !== null;
  useEffect(() => {
    if (wsReady && !device) {
      addToast('That device is no longer available.', 'info');
      navigate('/', { replace: true });
    }
  }, [wsReady, device, navigate, addToast]);

  if (!device) return null; // redirecting (or waiting for the first WS snapshot)

  // "Waiting" = trusted (stPaired) but never BEP-connected yet (accepted=false).
  const isWaiting = device.stPaired && !device.accepted;
  const statusLabel = isWaiting ? 'Waiting…' : device.connected ? 'Connected' : 'Offline';
  const statusColor = isWaiting
    ? 'text-blue-400'
    : device.connected
      ? 'text-emerald-500'
      : 'text-slate-400';

  return (
    <>
      <Breadcrumbs crumbs={[{ label: 'Devices', to: '/devices' }, { label: name }]} />
      <div className="max-w-xl mx-auto w-full px-4 py-6 sm:px-6 sm:py-8 flex flex-col gap-6">
        <Card className="px-5 py-4">
          <div className="flex items-start justify-between gap-4">
            <div>
              <h1 className="text-lg font-bold text-slate-900">{name}</h1>
              <p className="text-xs font-mono text-slate-400 mt-0.5">{id!.slice(0, 7)}</p>
              <p className={`text-xs mt-1 font-medium ${statusColor}`}>{statusLabel}</p>
            </div>
            {!isWaiting && (
              <AsyncButton variant="danger" size="sm" outlined onClick={handleRemoveTrust}>
                Remove
              </AsyncButton>
            )}
            {isWaiting && (
              <AsyncButton variant="danger" size="sm" outlined onClick={handleCancelRequest}>
                Cancel
              </AsyncButton>
            )}
          </div>

          {info && (
            <div className="mt-3 pt-3 border-t border-slate-100">
              <table className="w-full text-xs">
                <tbody>
                  <tr>
                    <td className="pr-4 py-0.5 text-slate-400">Host</td>
                    <td className="font-mono text-slate-600">{info.hostname}</td>
                  </tr>
                  <tr>
                    <td className="pr-4 py-0.5 text-slate-400">OS</td>
                    <td className="text-slate-600">
                      {OS_LABEL[info.os] ?? info.os}
                      {info.osVer && <span className="ml-1.5 text-slate-400">{info.osVer}</span>}
                    </td>
                  </tr>
                  {info.ifaces?.map((iface) => {
                    const ip = iface.ips.find((i) => !i.includes(':'));
                    if (!ip) return null;
                    return (
                      <tr key={iface.name}>
                        <td className="pr-4 py-0.5 text-slate-400">{iface.name}</td>
                        <td>
                          <span className="font-mono text-slate-600">{ip}</span>
                          {iface.mac && (
                            <div className="font-mono text-slate-300 text-[10px]">{iface.mac}</div>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </Card>

        {/* No shared folders nudge — only for trusted devices */}
        {sharedFolderCount === 0 && device.stPaired && (
          <Card className="px-4 py-6 sm:px-6 sm:py-8 flex flex-col items-center gap-4 text-center">
            <div className="w-11 h-11 rounded-full bg-amber-50 border border-amber-100 flex items-center justify-center">
              <FolderIcon className="w-5 h-5 text-amber-400" />
            </div>
            <div>
              <p className="text-sm font-semibold text-slate-700">Nothing synced with {name} yet</p>
              <p className="text-xs text-slate-400 mt-1">
                Go to Folders to share an existing folder, or create a new one.
              </p>
            </div>
            <Button onClick={() => navigate('/folders')}>Go to Folders</Button>
          </Card>
        )}
      </div>
    </>
  );
}
