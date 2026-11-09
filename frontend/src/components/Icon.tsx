import type { SVGProps } from 'react';

export type IconName =
  | 'alert'
  | 'arrow-down'
  | 'arrow-up'
  | 'check'
  | 'chevron-right'
  | 'clock'
  | 'copy'
  | 'download'
  | 'file'
  | 'folder'
  | 'history'
  | 'home'
  | 'image'
  | 'link'
  | 'message'
  | 'monitor'
  | 'moon'
  | 'more'
  | 'pause'
  | 'play'
  | 'plus'
  | 'refresh'
  | 'send'
  | 'settings'
  | 'shield'
  | 'smartphone'
  | 'sparkles'
  | 'sun'
  | 'trash'
  | 'upload'
  | 'wifi'
  | 'x';

type Props = SVGProps<SVGSVGElement> & { name: IconName; size?: number };

const paths: Record<IconName, React.ReactNode> = {
  alert: <><path d="M12 3 2.8 19a2 2 0 0 0 1.7 3h15a2 2 0 0 0 1.7-3L12 3Z" /><path d="M12 9v5" /><path d="M12 18h.01" /></>,
  'arrow-down': <><path d="M12 3v16" /><path d="m6 13 6 6 6-6" /></>,
  'arrow-up': <><path d="M12 21V5" /><path d="m18 11-6-6-6 6" /></>,
  check: <path d="m5 12 4 4L19 6" />,
  'chevron-right': <path d="m9 18 6-6-6-6" />,
  clock: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></>,
  copy: <><rect x="8" y="8" width="11" height="11" rx="2" /><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" /></>,
  download: <><path d="M12 3v12" /><path d="m7 10 5 5 5-5" /><path d="M5 21h14" /></>,
  file: <><path d="M6 2h8l4 4v16H6z" /><path d="M14 2v5h5" /></>,
  folder: <path d="M3 6a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />,
  history: <><path d="M3 12a9 9 0 1 0 3-6.7L3 8" /><path d="M3 3v5h5" /><path d="M12 7v5l3 2" /></>,
  home: <><path d="m3 11 9-8 9 8" /><path d="M5 10v11h14V10" /><path d="M9 21v-7h6v7" /></>,
  image: <><rect x="3" y="3" width="18" height="18" rx="3" /><circle cx="9" cy="9" r="2" /><path d="m21 15-4.5-4.5L6 21" /></>,
  link: <><path d="M10 13a5 5 0 0 0 7.5.5l2-2a5 5 0 0 0-7-7l-1.2 1.2" /><path d="M14 11a5 5 0 0 0-7.5-.5l-2 2a5 5 0 0 0 7 7l1.2-1.2" /></>,
  message: <><path d="M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z" /><path d="M8 9h8M8 13h5" /></>,
  monitor: <><rect x="3" y="4" width="18" height="13" rx="2" /><path d="M8 21h8M12 17v4" /></>,
  moon: <path d="M20 15.5A8.5 8.5 0 0 1 8.5 4 8.5 8.5 0 1 0 20 15.5Z" />,
  more: <><circle cx="5" cy="12" r="1" fill="currentColor" stroke="none" /><circle cx="12" cy="12" r="1" fill="currentColor" stroke="none" /><circle cx="19" cy="12" r="1" fill="currentColor" stroke="none" /></>,
  pause: <><path d="M9 5v14M15 5v14" /></>,
  play: <path d="m8 5 11 7-11 7z" />,
  plus: <><path d="M12 5v14M5 12h14" /></>,
  refresh: <><path d="M20 7v5h-5" /><path d="M4 17v-5h5" /><path d="M6.1 9A7 7 0 0 1 18 6l2 1M18 15a7 7 0 0 1-11.9 3L4 17" /></>,
  send: <><path d="m22 2-7 20-4-9-9-4z" /><path d="M22 2 11 13" /></>,
  settings: <><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1A1.7 1.7 0 0 0 9 4.6 1.7 1.7 0 0 0 10 3V2.8h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1Z" /></>,
  shield: <><path d="M12 22s8-4 8-11V5l-8-3-8 3v6c0 7 8 11 8 11Z" /><path d="m9 12 2 2 4-5" /></>,
  smartphone: <><rect x="6" y="2" width="12" height="20" rx="2" /><path d="M10 18h4" /></>,
  sparkles: <><path d="m12 3 1.1 3.4L16.5 7.5l-3.4 1.1L12 12l-1.1-3.4-3.4-1.1 3.4-1.1z" /><path d="m19 14 .7 2.3L22 17l-2.3.7L19 20l-.7-2.3L16 17l2.3-.7z" /><path d="m5 13 .7 2.3L8 16l-2.3.7L5 19l-.7-2.3L2 16l2.3-.7z" /></>,
  sun: <><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></>,
  trash: <><path d="M4 7h16M9 7V4h6v3M7 7l1 14h8l1-14M10 11v6M14 11v6" /></>,
  upload: <><path d="M12 16V4" /><path d="m7 9 5-5 5 5" /><path d="M5 21h14" /></>,
  wifi: <><path d="M2 8.5a15 15 0 0 1 20 0" /><path d="M5 12a10.5 10.5 0 0 1 14 0" /><path d="M8.5 15.5a5.2 5.2 0 0 1 7 0" /><circle cx="12" cy="19" r="1" fill="currentColor" stroke="none" /></>,
  x: <><path d="m6 6 12 12M18 6 6 18" /></>,
};

export function Icon({ name, size = 20, ...props }: Props) {
  return (
    <svg
      aria-hidden="true"
      fill="none"
      height={size}
      viewBox="0 0 24 24"
      width={size}
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth="1.8"
      {...props}
    >
      {paths[name]}
    </svg>
  );
}
