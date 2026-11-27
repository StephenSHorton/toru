// Pictures for image blocks, through Go's board.ImageService
// (internal/board/images.go): a file people choose or drop, or a Toru Library
// screenshot, copied into the board's images. The service is a React context
// so tests and pages without Go can give their own.
import * as React from "react";

type BoardImage = { asset: string; url: string; width: number; height: number };
type LibraryShot = { id: string; label: string; takenAt: string; url: string };

type BoardImages = {
  /** Where the window shows a board image. */
  url: (asset: string) => string;
  importFile: (file: File) => Promise<BoardImage>;
  library: () => Promise<LibraryShot[]>;
  importLibrary: (id: string) => Promise<BoardImage>;
};

const service = () => import("../../bindings/github.com/StephenSHorton/toru/internal/board/imageservice.js");

function toBase64(bytes: Uint8Array) {
  let out = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    out += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(out);
}

const goImages: BoardImages = {
  url: (asset) => `/__board/image/${encodeURIComponent(asset)}`,
  importFile: async (file) => (await service()).ImportData(toBase64(new Uint8Array(await file.arrayBuffer()))),
  library: async () =>
    (await (await service()).LibraryImages()).map((s) => ({ ...s, takenAt: String(s.takenAt) })),
  importLibrary: async (id) => (await service()).ImportLibrary(id),
};

const BoardImagesContext = React.createContext<BoardImages>(goImages);

const useBoardImages = () => React.useContext(BoardImagesContext);

export { BoardImagesContext, goImages, useBoardImages, type BoardImage, type BoardImages, type LibraryShot };
