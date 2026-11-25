import { useEffect, useMemo, useState } from 'react';
import {
  ArrowDownAZ,
  ArrowUp,
  ArrowUpZA,
  CheckSquare,
  FileVideo,
  Folder,
  FolderCheck,
  Loader2,
  Search,
  Square,
  Subtitles,
  X,
} from 'lucide-react';
import { api, formatBytes } from '../api/client';
import type { FSEntry } from '../types/api';

interface FileBrowserModalProps {
  open: boolean;
  initialPath?: string;
  onClose: () => void;
  onAddPaths: (paths: string[]) => Promise<void>;
}

export function FileBrowserModal({
  open,
  initialPath,
  onClose,
  onAddPaths,
}: FileBrowserModalProps) {
  const [currentPath, setCurrentPath] = useState(initialPath || '');
  const [parentPath, setParentPath] = useState('');
  const [entries, setEntries] = useState<FSEntry[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [pastedPaths, setPastedPaths] = useState('');
  const [filterText, setFilterText] = useState('');
  const [sortAsc, setSortAsc] = useState(true);
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  const loadDir = async (dirPath?: string) => {
    setLoading(true);
    setError('');
    try {
      const res = await api.browseFS(dirPath);
      setCurrentPath(res.path);
      setParentPath(res.parent);
      setEntries(res.entries || []);
      setFilterText('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to browse directory');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (open) {
      setSelected(new Set());
      setPastedPaths('');
      void loadDir(initialPath || undefined);
    }
  }, [open, initialPath]);

  const visibleEntries = useMemo(() => {
    const q = filterText.trim().toLowerCase();
    const filtered = q
      ? entries.filter((e) => e.name.toLowerCase().includes(q))
      : entries;

    return [...filtered].sort((a, b) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
      const cmp = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
      return sortAsc ? cmp : -cmp;
    });
  }, [entries, filterText, sortAsc]);

  if (!open) return null;

  const toggleSelect = (p: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(p)) next.delete(p);
      else next.add(p);
      return next;
    });
  };

  const handleConfirm = async (pathsToAdd: string[]) => {
    if (pathsToAdd.length === 0) return;
    setSubmitting(true);
    setError('');
    try {
      await onAddPaths(pathsToAdd);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add items');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm">
      <div className="flex max-h-[85vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-slate-800 bg-slate-900 shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-slate-800 px-5 py-4">
          <div>
            <h3 className="text-base font-semibold text-white">Add Files or Folders to Queue</h3>
            <p className="text-xs text-slate-400">
              Select individual videos or entire folders on the server/NAS.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-1.5 text-slate-400 hover:bg-slate-800 hover:text-white"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* Quick Paste Path(s) Bar (supports macOS Copy as Pathname) */}
        <div className="border-b border-slate-800 bg-slate-900/90 px-5 py-3">
          <div className="flex items-start gap-2">
            <textarea
              rows={2}
              value={pastedPaths}
              onChange={(e) => setPastedPaths(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                  const lines = pastedPaths
                    .split('\n')
                    .map((l) => l.trim())
                    .filter(Boolean);
                  void handleConfirm(lines);
                }
              }}
              placeholder="Paste file or folder path(s) here — e.g. '/Users/.../Downloads/Video.mkv' (one per line)"
              className="flex-1 resize-none rounded-lg border border-slate-700 bg-slate-950 px-3 py-1.5 font-mono text-xs text-slate-200 placeholder:text-slate-500 focus:border-indigo-500 focus:outline-none"
            />
            <button
              type="button"
              disabled={!pastedPaths.trim() || submitting}
              onClick={() => {
                const lines = pastedPaths
                  .split('\n')
                  .map((l) => l.trim())
                  .filter(Boolean);
                void handleConfirm(lines);
              }}
              className="shrink-0 rounded-lg bg-indigo-600 px-3.5 py-2 text-xs font-semibold text-white hover:bg-indigo-500 disabled:opacity-40"
            >
              Add Path(s)
            </button>
          </div>
        </div>

        {/* Path Navigation Bar */}
        <div className="flex items-center gap-2 border-b border-slate-800 bg-slate-950/60 px-5 py-2.5">
          <button
            type="button"
            disabled={!parentPath || loading}
            onClick={() => void loadDir(parentPath)}
            className="inline-flex items-center gap-1 rounded-lg bg-slate-800 px-2.5 py-1.5 text-xs font-medium text-slate-200 hover:bg-slate-700 disabled:opacity-40"
          >
            <ArrowUp className="h-3.5 w-3.5" />
            Up
          </button>
          <input
            type="text"
            value={currentPath}
            onChange={(e) => setCurrentPath(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void loadDir(currentPath);
            }}
            className="flex-1 rounded-lg border border-slate-800 bg-slate-900 px-3 py-1.5 text-xs text-slate-200 focus:border-indigo-500 focus:outline-none"
          />
          <button
            type="button"
            onClick={() => void loadDir(currentPath)}
            className="rounded-lg bg-slate-800 px-3 py-1.5 text-xs font-medium text-slate-200 hover:bg-slate-700"
          >
            Go
          </button>
        </div>

        {/* Filter & Sort Bar */}
        <div className="flex items-center gap-2 border-b border-slate-800/80 bg-slate-900/50 px-5 py-2">
          <div className="relative flex-1">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" />
            <input
              type="text"
              value={filterText}
              onChange={(e) => setFilterText(e.target.value)}
              placeholder="Filter files and folders by name..."
              className="w-full rounded-lg border border-slate-800 bg-slate-950 py-1.5 pl-8 pr-7 text-xs text-slate-200 placeholder:text-slate-500 focus:border-indigo-500 focus:outline-none"
            />
            {filterText && (
              <button
                type="button"
                onClick={() => setFilterText('')}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-200"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
          <button
            type="button"
            onClick={() => setSortAsc((v) => !v)}
            title={sortAsc ? 'Sorted by Name A → Z (click for Z → A)' : 'Sorted by Name Z → A (click for A → Z)'}
            className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-slate-800 bg-slate-950 px-2.5 py-1.5 text-xs font-medium text-slate-300 hover:border-slate-700 hover:text-white"
          >
            {sortAsc ? (
              <ArrowDownAZ className="h-3.5 w-3.5 text-indigo-400" />
            ) : (
              <ArrowUpZA className="h-3.5 w-3.5 text-indigo-400" />
            )}
            <span>{sortAsc ? 'Name A→Z' : 'Name Z→A'}</span>
          </button>
          <span className="shrink-0 text-[11px] text-slate-500">
            {visibleEntries.length}/{entries.length}
          </span>
        </div>

        {error && (
          <div className="mx-5 mt-3 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
            {error}
          </div>
        )}

        {/* Directory Listing */}
        <div className="flex-1 overflow-y-auto p-3">
          {loading ? (
            <div className="flex items-center justify-center py-16 text-slate-400">
              <Loader2 className="mr-2 h-5 w-5 animate-spin" />
              Loading directory...
            </div>
          ) : visibleEntries.length === 0 ? (
            <div className="py-16 text-center text-xs text-slate-500">
              {filterText
                ? `No items matching "${filterText}".`
                : 'No folders or supported video files found here.'}
            </div>
          ) : (
            <div className="space-y-1">
              {visibleEntries.map((entry) => {
                const isSelected = selected.has(entry.path);
                const selectable = entry.is_dir || entry.is_video;
                return (
                  <div
                    key={entry.path}
                    className={`flex items-center justify-between gap-3 rounded-lg px-3 py-2 text-sm transition ${
                      isSelected
                        ? 'bg-indigo-500/15 ring-1 ring-indigo-500/40'
                        : 'hover:bg-slate-800/70'
                    }`}
                  >
                    <div className="flex min-w-0 flex-1 items-center gap-2.5">
                      {selectable ? (
                        <button
                          type="button"
                          onClick={() => toggleSelect(entry.path)}
                          className="text-slate-400 hover:text-indigo-400"
                        >
                          {isSelected ? (
                            <CheckSquare className="h-4 w-4 text-indigo-400" />
                          ) : (
                            <Square className="h-4 w-4" />
                          )}
                        </button>
                      ) : (
                        <span className="w-4" />
                      )}

                      {entry.is_dir ? (
                        <button
                          type="button"
                          onClick={() => void loadDir(entry.path)}
                          className="flex min-w-0 flex-1 items-center gap-2 text-left text-slate-100 hover:text-indigo-300"
                        >
                          <Folder className="h-4 w-4 shrink-0 text-amber-400" />
                          <span className="truncate font-medium">{entry.name}</span>
                        </button>
                      ) : entry.is_video ? (
                        <button
                          type="button"
                          onClick={() => toggleSelect(entry.path)}
                          className="flex min-w-0 flex-1 items-center gap-2 text-left text-slate-200"
                        >
                          <FileVideo className="h-4 w-4 shrink-0 text-indigo-400" />
                          <span className="truncate">{entry.name}</span>
                        </button>
                      ) : (
                        <div className="flex min-w-0 flex-1 items-center gap-2 text-slate-500">
                          <Subtitles className="h-4 w-4 shrink-0" />
                          <span className="truncate text-xs">{entry.name}</span>
                        </div>
                      )}
                    </div>

                    {!entry.is_dir && (
                      <span className="shrink-0 text-xs text-slate-500">
                        {formatBytes(entry.size)}
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-slate-800 bg-slate-950/60 px-5 py-3.5">
          <button
            type="button"
            disabled={!currentPath || submitting}
            onClick={() => void handleConfirm([currentPath])}
            className="inline-flex items-center gap-1.5 rounded-lg bg-slate-800 px-3 py-2 text-xs font-medium text-slate-200 ring-1 ring-slate-700 hover:bg-slate-700 disabled:opacity-40"
          >
            <FolderCheck className="h-4 w-4 text-amber-400" />
            Add Entire Current Folder
          </button>

          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg px-3 py-2 text-xs font-medium text-slate-400 hover:text-slate-200"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={selected.size === 0 || submitting}
              onClick={() => void handleConfirm(Array.from(selected))}
              className="inline-flex items-center gap-1.5 rounded-lg bg-indigo-600 px-4 py-2 text-xs font-semibold text-white hover:bg-indigo-500 disabled:opacity-40"
            >
              {submitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Add Selected ({selected.size})
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
