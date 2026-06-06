import { useLocation, NavLink, Link } from 'react-router-dom';
import { useNavBadges } from '../../hooks/useNavBadges';

/** A single tab in the bottom nav. */
function Tab({
  active,
  badge,
  icon,
  label,
  to,
}: {
  active: boolean;
  badge: boolean;
  icon: React.ReactNode;
  label: string;
  to: string;
}) {
  return (
    <NavLink
      to={to}
      className={`flex flex-col items-center justify-center flex-1 gap-1 py-2 transition-colors ${
        active ? 'text-slate-900' : 'text-slate-400'
      }`}
    >
      <span className="relative">
        {icon}
        {badge && (
          <span className="absolute -top-0.5 -right-1 w-2 h-2 rounded-full bg-blue-400 animate-pulse" />
        )}
      </span>
      <span className="text-[10px] font-medium leading-none">{label}</span>
    </NavLink>
  );
}

/** Bottom navigation bar — visible on mobile only. */
export function BottomNav() {
  const { nearbyCount, pendingCount } = useNavBadges();
  const location = useLocation();

  const isDevices = location.pathname.startsWith('/device') || location.pathname === '/devices';
  const isFolders = location.pathname.startsWith('/folder') || location.pathname === '/folders';

  return (
    <nav className="sm:hidden fixed bottom-0 inset-x-0 bg-white border-t border-slate-200 flex safe-area-pb z-40">
      <Tab
        active={isDevices}
        badge={nearbyCount > 0}
        label="Devices"
        to="/devices"
        icon={
          <svg
            className="w-5 h-5"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.75"
          >
            <rect x="2" y="3" width="20" height="14" rx="2" />
            <path d="M8 21h8M12 17v4" strokeLinecap="round" />
          </svg>
        }
      />
      <Tab
        active={isFolders}
        badge={pendingCount > 0}
        label="Folders"
        to="/folders"
        icon={
          <svg
            className="w-5 h-5"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.75"
          >
            <path
              d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        }
      />
      {/* Gear icon — subtle settings link. flex-1 + same gap as the other
          tabs keeps the three columns evenly spaced; the muted color
          plus no badge is what makes it read as "secondary". */}
      <Link
        to="/settings"
        className="flex flex-col items-center justify-center flex-1 gap-1 py-2 text-slate-300 hover:text-slate-500 transition-colors"
        title="Settings"
      >
        <svg
          className="w-5 h-5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
        >
          <circle cx="12" cy="12" r="3" />
          <path
            d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        <span className="text-[10px] font-medium leading-none">Settings</span>
      </Link>
    </nav>
  );
}
