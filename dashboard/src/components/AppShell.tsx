import { Outlet } from 'react-router';
import { Header } from './Header';
import { Sidebar } from './Sidebar';

export function AppShell() {
  return (
    <div className="flex h-screen bg-background text-foreground">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <Header />
        <main className="flex-1 overflow-auto bg-surface p-6">
          <div className="mx-auto flex max-w-6xl flex-col gap-8">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
