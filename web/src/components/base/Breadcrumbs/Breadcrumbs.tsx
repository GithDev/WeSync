import { Link } from 'react-router-dom';

export interface Crumb {
  label: string;
  to?: string; // if omitted, this crumb is the current (non-clickable) page
}

interface Props {
  crumbs: Crumb[];
}

export function Breadcrumbs({ crumbs }: Props) {
  return (
    <nav className="flex items-center gap-1.5 text-sm text-slate-400 px-6 py-3 border-b border-slate-100 bg-white">
      {crumbs.map((crumb, i) => {
        const isLast = i === crumbs.length - 1;
        return (
          <span key={crumb.label} className="flex items-center gap-1.5">
            {i > 0 && <span className="text-slate-300">/</span>}
            {crumb.to && !isLast ? (
              <Link to={crumb.to} className="hover:text-slate-700 transition-colors">
                {crumb.label}
              </Link>
            ) : (
              <span className={isLast ? 'text-slate-700 font-medium' : ''}>{crumb.label}</span>
            )}
          </span>
        );
      })}
    </nav>
  );
}
