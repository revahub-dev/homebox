<script setup lang="ts">
  // Fork addition (photo-first capture). See FORK_NOTES.md.
  import { useI18n } from "vue-i18n";
  import { Button } from "@/components/ui/button";
  import { DialogRoot } from "reka-ui";
  import { DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
  import { Input } from "@/components/ui/input";
  import { Label } from "@/components/ui/label";
  import LocationSelector from "@/components/Location/Selector.vue";
  import type { CaptureItemOut, EntitySummary } from "~/lib/api/types/data-contracts";
  import type { CaptureEdit } from "~/lib/api/classes/x-capture";
  import { useFlatLocations } from "~~/composables/use-location-helpers";

  const props = defineProps<{ item: CaptureItemOut | null; open: boolean }>();
  const emit = defineEmits<{
    (e: "update:open", v: boolean): void;
    (e: "save", id: string, patch: CaptureEdit): void;
  }>();

  const { t } = useI18n();
  const locations = useFlatLocations();

  const form = reactive({ name: "", brand: "", modelNumber: "", category: "", quantity: 1 });
  const location = ref<EntitySummary | null>(null);

  watch(
    () => [props.open, props.item?.id] as const,
    ([open]) => {
      const it = props.item;
      if (!open || !it) return;
      form.name = it.name;
      form.brand = it.brand;
      form.modelNumber = it.modelNumber;
      form.category = it.category;
      form.quantity = it.quantity;
      const found = it.targetLocationId ? locations.value.find(l => l.id === it.targetLocationId) : undefined;
      location.value = found
        ? ({ id: found.id, name: found.name, treeString: found.treeString } as unknown as EntitySummary)
        : null;
    },
    { immediate: true }
  );

  const valid = computed(() => form.name.trim().length > 0 && Number(form.quantity) > 0);

  function save() {
    if (!props.item || !valid.value) return;
    const patch: CaptureEdit = {
      name: form.name.trim(),
      brand: form.brand.trim(),
      modelNumber: form.modelNumber.trim(),
      category: form.category.trim(),
      quantity: Number(form.quantity),
    };
    if (location.value) patch.targetLocationId = location.value.id;
    emit("save", props.item.id, patch);
    emit("update:open", false);
  }
</script>

<template>
  <DialogRoot :open="open" @update:open="v => emit('update:open', v)">
    <DialogContent class="max-w-md">
      <DialogHeader>
        <DialogTitle>{{ t("x_snap.edit.title") }}</DialogTitle>
      </DialogHeader>
      <form class="flex flex-col gap-3" @submit.prevent="save">
        <div class="flex flex-col gap-1">
          <Label for="snap-edit-name">{{ t("x_snap.edit.name") }}</Label>
          <Input id="snap-edit-name" v-model="form.name" maxlength="255" autocomplete="off" />
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div class="flex flex-col gap-1">
            <Label for="snap-edit-brand">{{ t("x_snap.edit.brand") }}</Label>
            <Input id="snap-edit-brand" v-model="form.brand" maxlength="255" />
          </div>
          <div class="flex flex-col gap-1">
            <Label for="snap-edit-model">{{ t("x_snap.edit.model") }}</Label>
            <Input id="snap-edit-model" v-model="form.modelNumber" maxlength="255" />
          </div>
          <div class="flex flex-col gap-1">
            <Label for="snap-edit-category">{{ t("x_snap.edit.category") }}</Label>
            <Input id="snap-edit-category" v-model="form.category" maxlength="255" />
          </div>
          <div class="flex flex-col gap-1">
            <Label for="snap-edit-qty">{{ t("x_snap.edit.quantity") }}</Label>
            <Input
              id="snap-edit-qty"
              v-model.number="form.quantity"
              type="number"
              min="1"
              step="1"
              inputmode="numeric"
            />
          </div>
        </div>
        <LocationSelector v-model="location" :label="t('x_snap.edit.location')" />
        <DialogFooter class="gap-2">
          <Button type="button" variant="ghost" @click="emit('update:open', false)">{{ t("global.cancel") }}</Button>
          <Button type="submit" :disabled="!valid">{{ t("global.save") }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </DialogRoot>
</template>
