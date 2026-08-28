<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6">
    <div class="flex flex-col gap-6 lg:flex-row lg:gap-8">
      <div class="lg:w-56 lg:shrink-0">
        <AdminNav />
      </div>
      <div class="min-w-0 flex-1 [&>*:first-child]:mt-0">
        <slot />
      </div>
    </div>
  </div>
</template>
