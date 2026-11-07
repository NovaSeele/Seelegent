/**
 * Field bindings for the automation editor's parts: each part gets the whole form as its v-model and binds its
 * inputs to `field('name')`, a writable computed that replaces the form with one field changed (the form is never
 * mutated in place, so the editor sees every change).
 */
import { computed, type Ref, type WritableComputedRef } from 'vue'

export function useFormFields<F extends object>(form: Ref<F>) {
  return function field<K extends keyof F>(key: K): WritableComputedRef<F[K]> {
    return computed({
      get: () => form.value[key],
      set: (value: F[K]) => {
        form.value = { ...form.value, [key]: value }
      },
    })
  }
}
