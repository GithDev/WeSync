import { useEffect } from 'react';
import { BrowserRouter, Routes, Route, Navigate, useNavigate } from 'react-router-dom';
import { SettingsPage } from './pages/SettingsPage/SettingsPage';
import { DevicePage } from './pages/DevicePage/DevicePage';
import { DevicesPage } from './pages/DevicesPage/DevicesPage';
import { FoldersPage } from './pages/FoldersPage/FoldersPage';
import { FolderPage } from './pages/FolderPage/FolderPage';
import { ToastProvider, useToast } from './components/base/Toast/Toast';
import { ConfirmProvider } from './components/base/ConfirmDialog/ConfirmDialog';
import { ErrorBoundary } from './components/base/ErrorBoundary/ErrorBoundary';
import { NavBar } from './components/layout/NavBar';
import { BottomNav } from './components/layout/BottomNav';
import { VisibilityBar } from './components/layout/VisibilityBar';
import { RouteBoundary } from './components/layout/RouteBoundary';
import { useWS } from './api/websocket';
import { homeTarget } from './state/home-route';
import { useNotifications } from './hooks/useNotifications';

function Notifications() {
  useNotifications();
  return null;
}

function Home() {
  const { devices } = useWS();
  return <Navigate to={homeTarget(devices)} replace />;
}

/** Any unknown path → home, with a heads-up. Resource pages (device/folder)
 *  handle their own "gone" redirect+toast; this catches everything else. */
function NotFoundRedirect() {
  const navigate = useNavigate();
  const { addToast } = useToast();
  useEffect(() => {
    addToast('That page doesn’t exist.', 'info');
    navigate('/', { replace: true });
  }, [navigate, addToast]);
  return null;
}

export function App() {
  return (
    <BrowserRouter>
      <ToastProvider>
        <ConfirmProvider>
          <ErrorBoundary scope="WeSync">
            <div className="min-h-screen bg-slate-50 font-sans flex flex-col">
              <NavBar />
              <VisibilityBar />
              <Notifications />
              {/* pb-16 on mobile gives room for the fixed bottom nav */}
              <main className="flex flex-col flex-1 pb-16 sm:pb-0">
                <RouteBoundary>
                  <Routes>
                    <Route path="/" element={<Home />} />
                    <Route path="/folders" element={<FoldersPage />} />
                    <Route path="/devices" element={<DevicesPage />} />
                    <Route path="/device/:id" element={<DevicePage />} />
                    <Route path="/folder/:id" element={<FolderPage />} />
                    <Route path="/settings" element={<SettingsPage />} />
                    <Route path="*" element={<NotFoundRedirect />} />
                  </Routes>
                </RouteBoundary>
              </main>
              <BottomNav />
            </div>
          </ErrorBoundary>
        </ConfirmProvider>
      </ToastProvider>
    </BrowserRouter>
  );
}
