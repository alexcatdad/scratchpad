// The Scratchpad notepad mark, shared with the macOS menu bar icon
// (macos/Resources/ScratchpadTrayIcon.svg). Ink on an amber tile.
export function Mark() {
  return (
    <span className="mark" aria-hidden="true">
      <svg viewBox="0 0 36 36" width="17" height="17" aria-hidden="true">
        <defs>
          <mask id="scratchpad-mark-ink">
            <rect width="36" height="36" fill="white" />
            <path
              d="M10 13h16"
              fill="none"
              stroke="black"
              strokeWidth="1.8"
              strokeLinecap="round"
            />
            <path
              d="M10 27c1.7-2.1 3.1-5.4 4.3-9.1M17 27.1c1.8-2.4 3.1-5.8 4.2-9.6M24 24.4c1.6-2.1 2.8-4.8 3.8-7.6"
              fill="none"
              stroke="black"
              strokeWidth="2.8"
              strokeLinecap="round"
            />
          </mask>
        </defs>
        <g fill="currentColor" mask="url(#scratchpad-mark-ink)">
          <path d="M10 6.5h16a3 3 0 0 1 3 3V32a2.5 2.5 0 0 1-2.5 2.5h-17A2.5 2.5 0 0 1 7 32V9.5a3 3 0 0 1 3-3Z" />
          <rect x="10.5" y="3" width="4" height="8" rx="2" />
          <rect x="21.5" y="3" width="4" height="8" rx="2" />
        </g>
      </svg>
    </span>
  );
}
