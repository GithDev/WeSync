import { useLocation } from 'react-router-dom';
import { ErrorBoundary } from '../base/ErrorBoundary/ErrorBoundary';

/** Route-scoped error boundary. Keyed on pathname so a crash on one page is
 *  cleared automatically when the user navigates elsewhere — no full reload
 *  needed. The outer boundary in main/App catches anything in the chrome
 *  (NavBar etc.) that would otherwise blank the whole window. */
export function RouteBoundary({ children }: { children: React.ReactNode }) {
  const location = useLocation();
  return (
    <ErrorBoundary key={location.pathname} scope="this page">
      {children}
    </ErrorBoundary>
  );
}
