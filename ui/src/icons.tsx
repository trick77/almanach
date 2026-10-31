// Copied from rongo (ui/src/icons.tsx), kept in step by hand.
// The two icons a diagram's tools use, no others.

export function ExpandIcon() {
  return (
    <svg
      className="h-[15px] w-[15px] shrink-0"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M6 2H2v4M10 2h4v4M6 14H2v-4M10 14h4v-4" />
    </svg>
  );
}

export function DownloadIcon() {
  return (
    <svg
      className="h-[15px] w-[15px] shrink-0"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M8 2v8M5 7.5 8 10.5 11 7.5M2.5 13h11" />
    </svg>
  );
}
