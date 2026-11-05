// The page's icons: one stroke, one weight, drawn in the text color.

import type { ReactNode } from "react";

const paths: Record<string, ReactNode> = {
  inbox: (
    <>
      <path d="M4 13.5 6.4 5.6A1.5 1.5 0 0 1 7.84 4.5h8.32a1.5 1.5 0 0 1 1.44 1.1L20 13.5" />
      <path d="M4 13.5V18a1.5 1.5 0 0 0 1.5 1.5h13A1.5 1.5 0 0 0 20 18v-4.5h-4.5l-1 2h-5l-1-2Z" />
    </>
  ),
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  cross: <path d="M6.5 6.5l11 11M17.5 6.5l-11 11" />,
  chat: (
    <>
      <path d="M5 5.5h14A1.5 1.5 0 0 1 20.5 7v8.5A1.5 1.5 0 0 1 19 17h-7l-4.5 3.5V17H5a1.5 1.5 0 0 1-1.5-1.5V7A1.5 1.5 0 0 1 5 5.5Z" />
      <path d="M8 10h8M8 13h5" />
    </>
  ),
  power: (
    <>
      <path d="M12 3.5V12" />
      <path d="M7.05 6.6a7 7 0 1 0 9.9 0" />
    </>
  ),
  folder: (
    <>
      <path d="M3.5 7A1.5 1.5 0 0 1 5 5.5h4.2l2 2.5H19A1.5 1.5 0 0 1 20.5 9.5V17A1.5 1.5 0 0 1 19 18.5H5A1.5 1.5 0 0 1 3.5 17Z" />
      <path d="M12 10.5v5M9.5 13h5" />
    </>
  ),
  key: (
    <>
      <circle cx="8" cy="15.5" r="4" />
      <path d="m10.9 12.6 8.6-8.6M16.5 7l2.5 2.5M14 9.5l2 2" />
    </>
  ),
  gear: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 3.5v2.2M12 18.3v2.2M20.5 12h-2.2M5.7 12H3.5M18 6l-1.55 1.55M7.55 16.45 6 18M18 18l-1.55-1.55M7.55 7.55 6 6" />
    </>
  ),
  reply: (
    <>
      <path d="M9.5 6.5 4.5 11l5 4.5" />
      <path d="M4.5 11H14a5.5 5.5 0 0 1 5.5 5.5v1" />
    </>
  ),
};

export type IconName = keyof typeof paths;

/** A 24-unit icon; size it with the em-based `.icon` rules or a parent's font-size. */
export function Icon({ name, className }: { name: IconName; className?: string }) {
  return (
    <svg className={`icon${className ? ` ${className}` : ""}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" aria-hidden focusable="false">
      {paths[name]}
    </svg>
  );
}

/** The tile that sits above an empty state's title. */
export function BigIcon({ name }: { name: IconName }) {
  return (
    <span className="big-icon">
      <Icon name={name} />
    </span>
  );
}
