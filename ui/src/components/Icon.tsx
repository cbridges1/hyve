// The console's hand-rolled 24x24 stroke icons.
const PATHS: Record<string, string[]> = {
  overview: ['M3 13h8V3H3zM13 21h8V11h-8zM3 21h8v-6H3zM13 3v6h8V3z'],
  clusters: ['M4 5a8 3 0 1 0 16 0a8 3 0 1 0-16 0', 'M4 5v6c0 1.66 3.58 3 8 3s8-1.34 8-3V5', 'M4 11v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6'],
  plus: ['M12 5v14M5 12h14'],
  templates: [
    'M4 3h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zM15 3h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1h-6a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zM4 14h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1zM15 14h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1h-6a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1z',
  ],
  resources: ['M12 3 20 7.5v9L12 21 4 16.5v-9Z', 'M4 7.5 12 12l8-4.5M12 12v9'],
  matrix: ['M4 4h16v16H4z', 'M4 9h16M9 4v16'],
  secrets: ['M7 11h10a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-5a2 2 0 0 1 2-2z', 'M8 11V7a4 4 0 0 1 8 0v4'],
  repos: ['M6 3v12', 'M18 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM6 21a3 3 0 1 0 0-6 3 3 0 0 0 0 6z', 'M18 9a9 9 0 0 1-9 9'],
  providers: ['M9 2v6M15 2v6M6 8h12v4a6 6 0 0 1-12 0zM12 18v4'],
  users: ['M12 4.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7z', 'M4.5 20c0-3.6 3.36-6.5 7.5-6.5s7.5 2.9 7.5 6.5'],
  environments: ['M12 3 21 8 12 13 3 8Z', 'M3 12l9 5 9-5', 'M3 16l9 5 9-5'],
  access: ['M12 3 5 6v5c0 4.5 3 8.3 7 9.5 4-1.2 7-5 7-9.5V6Z', 'M9 12l2 2 4-4'],
  import: ['M12 3v12', 'M7 10l5 5 5-5', 'M5 21h14'],
  menu: ['M4 6h16M4 12h16M4 18h16'],
  close: ['M6 6l12 12M18 6 6 18'],
  chevronRight: ['M9 6l6 6-6 6'],
  chevronDown: ['M6 9l6 6 6-6'],
  sun: [
    'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8z',
    'M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41',
  ],
  moon: ['M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z'],
  system: ['M4 4h16a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z', 'M8 20h8M12 16v4'],
  terminal: ['M4 17l6-5-6-5', 'M12 19h8'],
  logs: ['M4 6h16M4 10h16M4 14h10M4 18h7'],
  search: ['M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14z', 'M21 21l-4.3-4.3'],
  refresh: ['M20 11a8 8 0 0 0-14.9-3.9L3 9', 'M3 4v5h5', 'M4 13a8 8 0 0 0 14.9 3.9L21 15', 'M21 20v-5h-5'],
  download: ['M12 3v12', 'M7 10l5 5 5-5', 'M5 21h14'],
  edit: ['M4 20h4L19 9l-4-4L4 16z', 'M14 6l4 4'],
  trash: ['M4 7h16', 'M10 11v6M14 11v6', 'M6 7l1 13h10l1-13', 'M9 7V4h6v3'],
}

export function Icon({ name, size = 18, className }: { name: string; size?: number; className?: string }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className={className}
    >
      {(PATHS[name] || []).map((d) => (
        <path key={d} d={d} />
      ))}
    </svg>
  )
}
