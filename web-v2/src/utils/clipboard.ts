/** 复制文本到剪贴板;返回是否成功(失败时调用方提示手动选择)。 */
export function copyText(text: string): Promise<boolean> {
  if (!navigator.clipboard) return Promise.resolve(false);
  return navigator.clipboard.writeText(text).then(
    () => true,
    () => false,
  );
}
