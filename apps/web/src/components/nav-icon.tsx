const paths: Record<string, string> = {
  Memory:
    "M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z M14 3v6h6 M8 13h8 M8 17h6",
  Projects:
    "M3 7V5a2 2 0 0 1 2-2h5l2 4h7a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z",
  Insights: "M5 20v-6 M12 20V4 M19 20V9",
  Settings:
    "M10 2h4l.5 3.1 2 .9 2.6-1.7 2 3.4-2.2 2.1v2.3l2.2 2.1-2 3.4-2.6-1.7-2 .9L14 22h-4l-.5-3.1-2-.9-2.6 1.7-2-3.4 2.2-2.1V9.9L2.4 7.8l2-3.4L7 6.1l2-.9z M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8",
  Documentation: "M5 3h14v18H5z M9 7h6 M9 11h6 M9 15h4",
  "Sign out": "M9 4H4v16h5 M13 8l4 4-4 4 M8 12h13",
};

export function NavIcon({ name }: { name: string }) {
  return (
    <svg
      className="nav-icon"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={paths[name]} />
    </svg>
  );
}
