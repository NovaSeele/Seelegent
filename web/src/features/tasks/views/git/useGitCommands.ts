/** The Git view's commands: step through the changed files (j / k) and open the commit dialog. */
import { onBeforeUnmount } from 'vue'

import { useCommandsStore } from '@/stores/commands'

export function useGitCommands(
  hasFiles: () => boolean,
  step: (delta: number) => void,
  openCommit: () => void,
): void {
  const unregister = useCommandsStore().register(
    {
      id: 'git.nextFile',
      title: { key: 'git.cmd.next' },
      group: 'task',
      icon: 'chevronDown',
      shortcut: 'j',
      when: hasFiles,
      run: () => step(1),
    },
    {
      id: 'git.prevFile',
      title: { key: 'git.cmd.prev' },
      group: 'task',
      icon: 'chevronUp',
      shortcut: 'k',
      when: hasFiles,
      run: () => step(-1),
    },
    {
      id: 'git.commit',
      title: { key: 'git.cmd.commit' },
      group: 'task',
      icon: 'git',
      when: hasFiles,
      run: openCommit,
    },
  )
  onBeforeUnmount(unregister)
}
