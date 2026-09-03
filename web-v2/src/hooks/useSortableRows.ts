import { useCallback, useRef, useState } from 'react';

interface DropHint {
  index: number;
  after: boolean;
}

/**
 * 表格行拖拽排序。
 * - 只有按住 .gw-drag 手柄才允许拖动（mousedown 时动态设置 draggable）
 * - 落点提示用 2px 主色线，不做整行高亮或位移让位
 * - 手柄可 Tab 聚焦，Ctrl/Alt + ↑↓ 移动行（键盘无障碍底线）
 *
 * @param onMove 回调接收 (from, insertAt)，insertAt 为「插入到第几个位置之前」
 */
export function useSortableRows(onMove: (from: number, insertAt: number) => void) {
  const dragFrom = useRef<number | null>(null);
  const [dragging, setDragging] = useState<number | null>(null);
  const [hint, setHint] = useState<DropHint | null>(null);

  const reset = useCallback(() => {
    dragFrom.current = null;
    setDragging(null);
    setHint(null);
  }, []);

  const onRow = useCallback(
    (_record: unknown, index?: number) => {
      const i = index ?? 0;
      const cls = [
        dragging === i ? 'gw-dragging' : '',
        hint?.index === i ? (hint.after ? 'gw-drop-after' : 'gw-drop-before') : '',
      ].filter(Boolean).join(' ');

      return {
        className: cls,
        onMouseDown: (e: React.MouseEvent<HTMLElement>) => {
          const tr = e.currentTarget as HTMLTableRowElement;
          tr.draggable = !!(e.target as HTMLElement).closest?.('.gw-drag');
        },
        onDragStart: (e: React.DragEvent<HTMLElement>) => {
          if (!(e.currentTarget as HTMLTableRowElement).draggable) {
            e.preventDefault();
            return;
          }
          dragFrom.current = i;
          setDragging(i);
          e.dataTransfer.effectAllowed = 'move';
          try {
            e.dataTransfer.setData('text/plain', String(i));
          } catch {
            /* 某些浏览器限制 setData，忽略即可 */
          }
        },
        onDragOver: (e: React.DragEvent<HTMLElement>) => {
          if (dragFrom.current === null) return;
          e.preventDefault();
          e.dataTransfer.dropEffect = 'move';
          const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
          setHint({ index: i, after: e.clientY > rect.top + rect.height / 2 });
        },
        onDragLeave: () => setHint(h => (h?.index === i ? null : h)),
        onDrop: (e: React.DragEvent<HTMLElement>) => {
          if (dragFrom.current === null) return;
          e.preventDefault();
          const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
          const after = e.clientY > rect.top + rect.height / 2;
          const from = dragFrom.current;
          const insertAt = i + (after ? 1 : 0);
          reset();
          const target = insertAt > from ? insertAt - 1 : insertAt;
          if (from !== target) onMove(from, insertAt);
        },
        onDragEnd: reset,
      };
    },
    [dragging, hint, onMove, reset],
  );

  const handleProps = useCallback(
    (index: number) => ({
      className: 'gw-drag',
      title: '拖动调整顺序（或 Ctrl/Alt + ↑↓）',
      tabIndex: 0,
      onKeyDown: (e: React.KeyboardEvent<HTMLElement>) => {
        if (!e.ctrlKey && !e.altKey) return;
        if (e.key === 'ArrowUp' && index > 0) {
          e.preventDefault();
          onMove(index, index - 1);
        } else if (e.key === 'ArrowDown') {
          e.preventDefault();
          onMove(index, index + 2);
        }
      },
    }),
    [onMove],
  );

  return { onRow, handleProps };
}
