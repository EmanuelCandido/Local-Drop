import { useEffect, useRef } from 'react';
import { Icon } from './Icon';

type Props = {
  title: string;
  text: string;
  onCopy: () => void;
  onClose: () => void;
};

const linkPattern = /^https?:\/\/\S+$/i;

export function TextPreview({ title, text, onCopy, onClose }: Props) {
  const dialog = useRef<HTMLDialogElement>(null);
  const trimmed = text.trim();
  const isLink = linkPattern.test(trimmed);

  useEffect(() => {
    const element = dialog.current;
    if (element && !element.open) element.showModal?.();
    return () => element?.close?.();
  }, []);

  return (
    <dialog ref={dialog} className="sheet" aria-labelledby="text-preview-title" onClose={onClose} onCancel={onClose}>
      <div className="sheet-header">
        <h2 id="text-preview-title">{title}</h2>
        <button type="button" className="icon-button" aria-label="Fechar" onClick={onClose}><Icon name="x" size={20} /></button>
      </div>
      <pre className="text-preview">{text}</pre>
      <div className="sheet-actions">
        {isLink && (
          <a className="button-secondary" href={trimmed} target="_blank" rel="noopener noreferrer">
            <Icon name="link" size={17} /> Abrir link
          </a>
        )}
        <button type="button" className="button-primary" onClick={onCopy}><Icon name="copy" size={17} /> Copiar</button>
      </div>
    </dialog>
  );
}
