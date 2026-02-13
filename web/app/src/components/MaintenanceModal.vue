<template>
  <div v-if="visible" class="fixed inset-0 z-50 flex items-center justify-center">
    <!-- Backdrop -->
    <div class="fixed inset-0 bg-black/50" @click="close"></div>

    <!-- Modal -->
    <div class="relative z-10 w-full max-w-lg mx-4 bg-popover text-popover-foreground border rounded-lg shadow-lg">
      <!-- Header -->
      <div class="flex items-center justify-between p-4 border-b">
        <h2 class="text-lg font-semibold">New Maintenance Window</h2>
        <Button variant="ghost" size="icon" @click="close">
          <X class="h-4 w-4" />
        </Button>
      </div>

      <!-- Body -->
      <div class="p-4 space-y-4 max-h-[70vh] overflow-y-auto">
        <!-- Start Time -->
        <div>
          <label class="block text-sm font-medium mb-1">Start Time</label>
          <Input
            type="datetime-local"
            v-model="startTime"
            class="w-full"
          />
        </div>

        <!-- End Time -->
        <div>
          <label class="block text-sm font-medium mb-1">End Time</label>
          <Input
            type="datetime-local"
            v-model="endTime"
            class="w-full"
          />
        </div>

        <!-- Mode Selector -->
        <div>
          <label class="block text-sm font-medium mb-1">Apply to</label>
          <div class="flex gap-2">
            <Button
              :variant="mode === 'groups' ? 'default' : 'outline'"
              size="sm"
              @click="mode = 'groups'"
            >
              Group Prefixes
            </Button>
            <Button
              :variant="mode === 'endpoints' ? 'default' : 'outline'"
              size="sm"
              @click="mode = 'endpoints'"
            >
              Endpoints
            </Button>
          </div>
        </div>

        <!-- Group Prefixes -->
        <div v-if="mode === 'groups'">
          <label class="block text-sm font-medium mb-1">Group Prefixes</label>
          <Input
            v-model="groupPrefixesInput"
            placeholder="e.g. akrikhin-test, albaker-test"
            class="w-full"
          />
          <p class="text-xs text-muted-foreground mt-1">Comma-separated group prefixes</p>
          <!-- Quick-select chips from available groups -->
          <div v-if="availableGroups.length > 0" class="flex flex-wrap gap-1 mt-2">
            <button
              v-for="group in availableGroups"
              :key="group"
              @click="toggleGroupPrefix(group)"
              :class="[
                'px-2 py-0.5 rounded-full text-xs border transition-colors',
                selectedGroupSet.has(group)
                  ? 'bg-primary text-primary-foreground border-primary'
                  : 'bg-background text-foreground border-input hover:bg-accent'
              ]"
            >
              {{ group }}
            </button>
          </div>
        </div>

        <!-- Endpoint Selection -->
        <div v-if="mode === 'endpoints'">
          <label class="block text-sm font-medium mb-1">Endpoints</label>
          <Input
            v-model="endpointSearch"
            placeholder="Search endpoints..."
            class="w-full mb-2"
          />
          <div class="border rounded-md max-h-48 overflow-y-auto">
            <label
              v-for="ep in filteredEndpoints"
              :key="ep.key"
              class="flex items-center gap-2 px-3 py-1.5 hover:bg-accent cursor-pointer text-sm"
            >
              <input
                type="checkbox"
                :value="ep.key"
                v-model="selectedEndpointKeys"
                class="rounded border-input"
              />
              <span class="text-muted-foreground text-xs">{{ ep.group }}/</span>
              <span>{{ ep.name }}</span>
            </label>
            <div v-if="filteredEndpoints.length === 0" class="px-3 py-2 text-sm text-muted-foreground">
              No endpoints match
            </div>
          </div>
          <p class="text-xs text-muted-foreground mt-1">{{ selectedEndpointKeys.length }} selected</p>
        </div>

        <!-- Note -->
        <div>
          <label class="block text-sm font-medium mb-1">Note (optional)</label>
          <Input
            v-model="note"
            placeholder="Reason for maintenance..."
            class="w-full"
          />
        </div>

        <!-- Error -->
        <div v-if="error" class="p-3 rounded-md bg-destructive/10 border border-destructive/20">
          <p class="text-sm text-destructive">{{ error }}</p>
        </div>
      </div>

      <!-- Footer -->
      <div class="flex items-center justify-end gap-2 p-4 border-t">
        <Button variant="outline" @click="close" :disabled="submitting">Cancel</Button>
        <Button @click="submit" :disabled="submitting">
          <span v-if="submitting">Creating...</span>
          <span v-else>Create</span>
        </Button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { X } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const props = defineProps({
  visible: { type: Boolean, default: false },
  endpoints: { type: Array, default: () => [] },
})

const emit = defineEmits(['close', 'created'])

const mode = ref('groups')
const startTime = ref('')
const endTime = ref('')
const groupPrefixesInput = ref('')
const selectedEndpointKeys = ref([])
const endpointSearch = ref('')
const note = ref('')
const error = ref('')
const submitting = ref(false)

const availableGroups = computed(() => {
  const groups = new Set()
  props.endpoints.forEach(ep => {
    if (ep.group) groups.add(ep.group)
  })
  return Array.from(groups).sort()
})

const selectedGroupSet = computed(() => {
  const prefixes = groupPrefixesInput.value
    .split(',')
    .map(s => s.trim())
    .filter(Boolean)
  return new Set(prefixes)
})

const filteredEndpoints = computed(() => {
  const q = endpointSearch.value.toLowerCase()
  return props.endpoints.filter(ep => {
    if (!q) return true
    return (
      ep.name.toLowerCase().includes(q) ||
      (ep.group && ep.group.toLowerCase().includes(q)) ||
      ep.key.toLowerCase().includes(q)
    )
  })
})

const toggleGroupPrefix = (group) => {
  const current = groupPrefixesInput.value
    .split(',')
    .map(s => s.trim())
    .filter(Boolean)
  const idx = current.indexOf(group)
  if (idx >= 0) {
    current.splice(idx, 1)
  } else {
    current.push(group)
  }
  groupPrefixesInput.value = current.join(', ')
}

const toRFC3339 = (localDatetime) => {
  if (!localDatetime) return null
  return new Date(localDatetime).toISOString()
}

const resetForm = () => {
  mode.value = 'groups'
  startTime.value = ''
  endTime.value = ''
  groupPrefixesInput.value = ''
  selectedEndpointKeys.value = []
  endpointSearch.value = ''
  note.value = ''
  error.value = ''
  submitting.value = false
}

const close = () => {
  resetForm()
  emit('close')
}

// Reset form when modal opens
watch(() => props.visible, (val) => {
  if (val) {
    resetForm()
    // Default start to now, end to +2h
    const now = new Date()
    const later = new Date(now.getTime() + 2 * 60 * 60 * 1000)
    startTime.value = toLocalDatetime(now)
    endTime.value = toLocalDatetime(later)
  }
})

const toLocalDatetime = (date) => {
  const pad = (n) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

const submit = async () => {
  error.value = ''

  if (!startTime.value || !endTime.value) {
    error.value = 'Start and end times are required'
    return
  }

  const start = toRFC3339(startTime.value)
  const end = toRFC3339(endTime.value)

  if (new Date(start) >= new Date(end)) {
    error.value = 'End time must be after start time'
    return
  }

  submitting.value = true

  try {
    if (mode.value === 'groups') {
      const groups = groupPrefixesInput.value
        .split(',')
        .map(s => s.trim())
        .filter(Boolean)
      if (groups.length === 0) {
        error.value = 'At least one group prefix is required'
        submitting.value = false
        return
      }
      const resp = await fetch('/api/v1/maintenances/by-groups', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          groups,
          start_timestamp: start,
          end_timestamp: end,
          note: note.value,
        }),
      })
      if (!resp.ok) {
        error.value = await resp.text()
        submitting.value = false
        return
      }
    } else {
      if (selectedEndpointKeys.value.length === 0) {
        error.value = 'At least one endpoint must be selected'
        submitting.value = false
        return
      }
      const resp = await fetch('/api/v1/maintenances/by-keys', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          endpoint_keys: selectedEndpointKeys.value,
          start_timestamp: start,
          end_timestamp: end,
          note: note.value,
        }),
      })
      if (!resp.ok) {
        error.value = await resp.text()
        submitting.value = false
        return
      }
    }

    emit('created')
    close()
  } catch (err) {
    error.value = 'Failed to create maintenance window: ' + err.message
    submitting.value = false
  }
}
</script>
