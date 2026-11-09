import { useEffect, useState } from "react";
import { Icon } from "../icons";
import { feedback, type Attachment } from "../mygo";

/**
 * The pictures attached to an answered Ask, as the small JPEGs the broker shrinks them to (a few KB each — never the
 * 4 MiB originals), in the order they were attached. One read per Ask for the life of the page; while it is in
 * flight the tiles are placeholders of the count the Ask already carries, so the bubble does not move when they
 * arrive. A read that fails or returns nothing (an older Mac) draws nothing: the "N images" caption stays the record.
 */

const cache = new Map<string, Promise<Attachment[]>>();

function read(askId: string): Promise<Attachment[]> {
  let p = cache.get(askId);
  if (!p) {
    p = feedback.thumbnails(askId).catch((e) => {
      cache.delete(askId); // a failed read is tried again the next time the Ask is drawn
      throw e;
    });
    cache.set(askId, p);
  }
  return p;
}

export function Thumbs(props: { askId: string; count: number }) {
  const { askId, count } = props;
  const [tiles, setTiles] = useState<Attachment[] | null>(null);
  useEffect(() => {
    let live = true;
    read(askId)
      .then((t) => live && setTiles(t))
      .catch(() => live && setTiles([]));
    return () => {
      live = false;
    };
  }, [askId]);
  if (tiles && tiles.length === 0) return null;
  const slots = tiles ?? Array.from({ length: count }, (): Attachment | null => null);
  return (
    <div className="fb-thumbs">
      {slots.map((t, i) =>
        t && t.dataBase64 ? (
          <img className="fb-thumb" key={i} src={`data:${t.mimeType};base64,${t.dataBase64}`} alt={t.name || "Attached image"} title={t.name} draggable={false} />
        ) : (
          <span className={`fb-thumb ${t ? "gone" : "wait"}`} key={i} title={t?.name}>
            {t && <Icon name="image" size={18} />}
          </span>
        ),
      )}
    </div>
  );
}
