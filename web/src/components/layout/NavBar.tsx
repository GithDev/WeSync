import { useNavigate, NavLink } from 'react-router-dom';
import { useNavBadges } from '../../hooks/useNavBadges';

export function NavBar() {
  const { nearbyCount, pendingCount } = useNavBadges();
  const navigate = useNavigate();

  return (
    <nav className="bg-white border-b border-slate-200 px-4 sm:px-6">
      <div className="flex items-center h-14 gap-4 sm:gap-6">
        {/* Logo */}
        <button
          type="button"
          onClick={() => navigate('/')}
          className="font-bold text-base text-slate-900 flex-shrink-0"
        >
          WeSync
        </button>

        {/* Desktop nav links — hidden on mobile (bottom nav handles it) */}
        <div className="hidden sm:flex items-center gap-5">
          <span className="relative">
            <NavLink
              to="/devices"
              className={({ isActive }) =>
                `text-sm font-medium transition-colors px-1 py-0.5 border-b-2 ${isActive ? 'border-slate-800 text-slate-900' : 'border-transparent text-slate-400 hover:text-slate-700'}`
              }
            >
              Devices
            </NavLink>
            {nearbyCount > 0 && (
              <span className="absolute -top-0.5 -right-2.5 w-2 h-2 rounded-full bg-blue-400 animate-pulse" />
            )}
          </span>
          <span className="relative">
            <NavLink
              to="/folders"
              className={({ isActive }) =>
                `text-sm font-medium transition-colors px-1 py-0.5 border-b-2 ${isActive ? 'border-slate-800 text-slate-900' : 'border-transparent text-slate-400 hover:text-slate-700'}`
              }
            >
              Folders
            </NavLink>
            {pendingCount > 0 && (
              <span className="absolute -top-0.5 -right-2.5 w-2 h-2 rounded-full bg-blue-400 animate-pulse" />
            )}
          </span>
        </div>

        {/* Settings — desktop only (mobile uses bottom nav gear) */}
        <NavLink
          to="/settings"
          className={({ isActive }) =>
            `ml-auto hidden sm:flex items-center text-slate-300 hover:text-slate-500 transition-colors ${isActive ? 'text-slate-600' : ''}`
          }
          title="Settings"
        >
          <svg
            className="w-4 h-4"
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
        </NavLink>
      </div>
    </nav>
  );
}
