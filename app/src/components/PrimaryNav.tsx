import { NavLink } from 'react-router';

// The inbox (N1) has no page yet; add it here when it lands.
const ITEMS = [
  { to: '/today', label: 'Today' },
  { to: '/path', label: 'Path' },
  { to: '/practice', label: 'Practice' },
] as const;

/** Top-bar links on desktop. */
export function TopNav() {
  return (
    <nav aria-label="Primary" className="hidden gap-1 md:flex">
      {ITEMS.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          className={({ isActive }) =>
            `inline-flex h-10 items-center rounded-[10px] px-3.5 text-sm font-semibold no-underline ${
              isActive ? 'bg-accentsoft text-accentink' : 'text-fg2 hover:text-fg'
            }`
          }
        >
          {item.label}
        </NavLink>
      ))}
    </nav>
  );
}

/** Bottom tab bar on the phone. */
export function TabBar() {
  return (
    <nav
      aria-label="Primary, tabs"
      className="fixed inset-x-0 bottom-0 z-10 grid border-t border-line bg-bg px-2 pt-1 pb-5 md:hidden"
      style={{ gridTemplateColumns: `repeat(${ITEMS.length}, 1fr)` }}
    >
      {ITEMS.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          className={({ isActive }) =>
            `flex h-12 items-center justify-center text-xs font-bold no-underline ${isActive ? 'text-accentink' : 'text-muted'}`
          }
        >
          {item.label}
        </NavLink>
      ))}
    </nav>
  );
}
