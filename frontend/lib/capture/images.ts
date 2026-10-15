// Fork addition (photo-first capture). See FORK_NOTES.md.
// Client-side helpers for the snap screen: resize before upload so phone
// photos (often 4-12 MB) become ~200-400 KB, and a tiny upload queue.

export const MAX_EDGE = 1600;
export const JPEG_QUALITY = 0.8;

/** Scales (w, h) so the long edge is at most maxEdge; never upscales. */
export function fitSize(w: number, h: number, maxEdge = MAX_EDGE): { width: number; height: number } {
  if (w <= 0 || h <= 0) return { width: 0, height: 0 };
  const long = Math.max(w, h);
  if (long <= maxEdge) return { width: Math.round(w), height: Math.round(h) };
  const k = maxEdge / long;
  return { width: Math.max(1, Math.round(w * k)), height: Math.max(1, Math.round(h * k)) };
}

/** True for files the picker may hand us that we can send as photos. */
export function isImageFile(f: { type: string; name: string }): boolean {
  if (f.type.startsWith("image/")) return true;
  // Some Android galleries report HEIC/HEIF with an empty type.
  return f.type === "" && /\.(jpe?g|png|webp|heic|heif)$/i.test(f.name);
}

/**
 * Draws a decoded image or video frame into a JPEG blob with a long edge of
 * at most maxEdge. EXIF orientation is applied by the browser when the source
 * is an ImageBitmap created with imageOrientation "from-image".
 */
export async function drawToJpeg(
  source: CanvasImageSource,
  srcW: number,
  srcH: number,
  maxEdge = MAX_EDGE,
  quality = JPEG_QUALITY
): Promise<Blob> {
  const { width, height } = fitSize(srcW, srcH, maxEdge);
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("canvas unavailable");
  ctx.drawImage(source, 0, 0, width, height);
  return await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob(b => (b ? resolve(b) : reject(new Error("encode failed"))), "image/jpeg", quality);
  });
}

/**
 * Resizes a gallery file. If the browser cannot decode it (e.g. HEIC in
 * Chrome) the original is returned and the server decodes it instead.
 */
export async function resizeFile(file: File, maxEdge = MAX_EDGE, quality = JPEG_QUALITY): Promise<Blob> {
  try {
    const bmp = await createImageBitmap(file, { imageOrientation: "from-image" });
    try {
      return await drawToJpeg(bmp, bmp.width, bmp.height, maxEdge, quality);
    } finally {
      bmp.close();
    }
  } catch {
    return file;
  }
}

/**
 * Runs async jobs with at most `limit` in flight. Jobs are started in order;
 * the returned promise settles when all have finished (errors are the job's
 * own business: a rejecting job does not stop the queue).
 */
export async function runLimited<T>(items: T[], limit: number, job: (item: T) => Promise<void>): Promise<void> {
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const item = items[next++] as T;
      try {
        await job(item);
      } catch {
        // job reports its own failure
      }
    }
  };
  await Promise.all(Array.from({ length: Math.max(1, Math.min(limit, items.length)) }, worker));
}

export type Bucket = "ready" | "review" | "maybe";

/** Human-friendly percent for the confidence chip. */
export function confidencePercent(c: number): number {
  return Math.round(Math.min(1, Math.max(0, c)) * 100);
}
