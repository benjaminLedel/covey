import { useTranslation } from "react-i18next";
import { CHAT_MAX_BYTES, CHAT_MAX_FILES, chatAttachmentUrl, type ChatAttachment } from "../api";
import { NavIcon } from "../components/navicons";

/* The files a message carries (#586), at the message: an image as a picture,
 * a video as a player, anything else as a chip that downloads. What the
 * server shows in place it decides itself (chatInlineType); the surface only
 * picks the element. */

const size = (n: number) =>
  n < 1024 ? `${n} B` : n < 1048576 ? `${Math.round(n / 1024)} KB` : `${(n / 1048576).toFixed(1)} MB`;

const isImage = (a: ChatAttachment) => a.mime.startsWith("image/");
const isVideo = (a: ChatAttachment) => a.mime === "video/mp4" || a.mime === "video/webm";

export default function Attachments({ files }: { files?: ChatAttachment[] }) {
  const { t } = useTranslation();
  if (!files || files.length === 0) return null;
  return (
    <div className="tm-files">
      {files.map((a) =>
        /* Still on its way: the message is shown before the server has it,
           and the file has no address yet. */
        !a.mime ? (
          <span key={a.id} className="tm-anhang tm-file">
            <NavIcon name="clip" />
            <span className="tm-anhang-name">{a.name}</span>
          </span>
        ) : isImage(a) ? (
          <a key={a.id} className="tm-file-image" href={chatAttachmentUrl(a.id)} target="_blank" rel="noreferrer" title={a.name}>
            <img src={chatAttachmentUrl(a.id)} alt={a.name} loading="lazy" />
          </a>
        ) : isVideo(a) ? (
          <video key={a.id} className="tm-file-video" src={chatAttachmentUrl(a.id)} controls preload="metadata" title={a.name} />
        ) : (
          <a
            key={a.id}
            className="tm-anhang tm-file"
            href={chatAttachmentUrl(a.id, true)}
            download={a.name}
            title={t("chat.files.download", { name: a.name })}
          >
            <NavIcon name="clip" />
            <span className="tm-anhang-name">{a.name}</span>
            <span className="tm-leise">{size(a.size)}</span>
          </a>
        ),
      )}
    </div>
  );
}

/** Holds the files chosen for a message to the server's limits; answers the
 *  sentence to show when they do not fit, or "" when they do. */
export function attachmentLimit(t: (k: string, o?: Record<string, unknown>) => string, files: File[]): string {
  if (files.length > CHAT_MAX_FILES) return t("chat.files.tooMany", { max: CHAT_MAX_FILES });
  const total = files.reduce((n, f) => n + f.size, 0);
  if (total > CHAT_MAX_BYTES) return t("chat.files.tooLarge", { max: CHAT_MAX_BYTES / 1048576 });
  return "";
}
