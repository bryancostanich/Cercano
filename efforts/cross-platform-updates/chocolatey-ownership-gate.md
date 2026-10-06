# Chocolatey self-update opt-in — ownership contract approved

The approved spec allows explicit per-user opt-in to self-updates while package
management remains the default. The user explicitly approved retaining Chocolatey
registration with a cooperating installer/shared coordinator. This resolves the
Phase3 ownership-contract design gate, not authorization to migrate any live
installation or edit Chocolatey's database.

Approved: keep Chocolatey registration and package-based uninstall, but have
a cooperating Cercano installer/package and the self-updater use the same Go
coordinator. Record explicit delegated update ownership for the exact enrolled
installation. A future Chocolatey action must honor that record, inspect actual
installed version, avoid downgrades and never independently overwrite files
beneath the coordinator. Chocolatey Open Source package records can lag after a
self-update; package upgrade can reconcile them without downgrading application
content. This must be tested against real Chocolatey, not just a mocked record.

Do not offer the opt-in for an arbitrary Chocolatey installation whose package
lacks this contract. Do not modify Chocolatey's database from the application.
Default package-managed installations remain unchanged; machine-wide and
policy-prohibited self-updates remain excluded.

Alternative: explicitly convert to a standalone self-managed installation and
remove Chocolatey registration via supported package operations. This simplifies
long-term ownership but is a real user-visible migration: Chocolatey would no
longer upgrade or uninstall Cercano. Migration failure/recovery needs a separate
safe sequence and cannot be hidden behind a checkbox.

Registration retention is adopted; conversion/removal was not selected. The
classifier must continue rejecting simultaneous ownership claims unless a
complete, identity-bound, explicitly consented delegation and supporting
installer contract are corroborated. A global enrollment flag is insufficient.
Policy records follow this approved contract, and platform probes must establish
the actual contract before any real files are writable through self-update.
