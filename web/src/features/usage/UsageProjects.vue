<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Summary } from '@/api/types'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'

/**
 * The projects that used the most tokens, each opening to its top tasks and each task to its turns. The daemon
 * sends the top few of every level; how many more there are is said under each list.
 */
const props = defineProps<{ summary: Summary }>()

const { t } = useI18n()
const fmt = useFormat()
const open = ref(new Set<string>())

function toggle(key: string) {
  const next = new Set(open.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  open.value = next
}

const cost = (n: number) =>
  props.summary.totals.hasCost ? fmt.value.number(n, { style: 'currency', currency: 'USD' }) : '—'
</script>

<template>
  <section class="usage-table" aria-labelledby="usage-projects">
    <h2 id="usage-projects" class="usage-table__title">{{ t('usage.projects.title') }}</h2>
    <p v-if="!summary.projects.length" class="usage-table__empty">
      {{ t('usage.projects.empty') }}
    </p>
    <div v-else class="usage-table__scroll">
      <table>
        <thead>
          <tr>
            <th scope="col">{{ t('usage.col.project') }}</th>
            <th scope="col">{{ t('usage.col.tokens') }}</th>
            <th scope="col">{{ t('usage.col.cost') }}</th>
            <th scope="col">{{ t('usage.col.tasks') }}</th>
            <th scope="col">{{ t('usage.col.time') }}</th>
          </tr>
        </thead>
        <tbody v-for="project in summary.projects" :key="project.key">
          <tr class="lvl-0">
            <th scope="row">
              <button
                type="button"
                class="disclose"
                :aria-expanded="open.has(project.key)"
                @click="toggle(project.key)"
              >
                <Icon :name="open.has(project.key) ? 'chevronDown' : 'chevronRight'" />
                {{ project.name }}
              </button>
            </th>
            <td>{{ fmt.compact(project.tokens) }}</td>
            <td>{{ cost(project.costUsd) }}</td>
            <td>{{ fmt.number(project.tasks) }}</td>
            <td>{{ fmt.duration(project.durationMs) }}</td>
          </tr>
          <template v-if="open.has(project.key)">
            <template v-for="task in project.items" :key="task.id">
              <tr class="lvl-1">
                <th scope="row">
                  <span class="cell">
                    <button
                      type="button"
                      class="disclose"
                      :aria-expanded="open.has(task.id)"
                      :aria-label="t('usage.showTurns', { title: task.title })"
                      @click="toggle(task.id)"
                    >
                      <Icon :name="open.has(task.id) ? 'chevronDown' : 'chevronRight'" />
                    </button>
                    <RouterLink class="cell__link" :to="`/tasks/${encodeURIComponent(task.id)}`">
                      {{ task.title }}
                    </RouterLink>
                  </span>
                </th>
                <td>{{ fmt.compact(task.tokens) }}</td>
                <td>{{ cost(task.costUsd) }}</td>
                <td>{{ t('usage.turns', { n: task.turns }, task.turns) }}</td>
                <td>{{ fmt.duration(task.durationMs) }}</td>
              </tr>
              <template v-if="open.has(task.id)">
                <tr v-for="turn in task.items" :key="`${task.id}.${turn.n}`" class="lvl-2">
                  <th scope="row">
                    {{ t('usage.turn', { n: turn.n }) }} · {{ turn.account }} · {{ turn.model }}
                  </th>
                  <td>{{ fmt.compact(turn.tokens) }}</td>
                  <td>{{ cost(turn.costUsd) }}</td>
                  <td>{{ t('usage.attempts', { n: turn.attempts }, turn.attempts) }}</td>
                  <td>{{ fmt.duration(turn.durationMs) }}</td>
                </tr>
                <tr v-if="task.more" class="lvl-2 more">
                  <td colspan="5">{{ t('usage.moreTurns', { n: task.more }, task.more) }}</td>
                </tr>
              </template>
            </template>
            <tr v-if="project.more" class="lvl-1 more">
              <td colspan="5">{{ t('usage.moreTasks', { n: project.more }, project.more) }}</td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
    <p v-if="summary.moreProjects" class="usage-table__more">
      {{ t('usage.moreProjects', { n: summary.moreProjects }, summary.moreProjects) }}
    </p>
  </section>
</template>

<style scoped src="./usageTable.css"></style>
