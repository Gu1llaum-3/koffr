# Architecture Decision Records

Une décision structurante = un fichier `NNNN-titre-kebab.md`, numéro croissant, jamais réutilisé.
On ne modifie pas un ADR accepté : on en écrit un nouveau qui le remplace ou l'amende, et on met à
jour le statut de l'ancien dans cette table. Gabarit : `0000-template.md`. Skill : `/ecrire-adr`.

Un ADR **proposé** par Claude (depuis le cahier des charges ou en cours de lot) ne vaut rien tant
que le propriétaire ne l'a pas passé en **accepté** ; on ne code pas dessus.

| #    | Titre | Statut | Date |
| ---- | ----- | ------ | ---- |
| 0001 | Le produit s'appelle koffr, sous licence Apache-2.0, dans deux dépôts | accepté | 2026-09-18 |
| 0002 | La pile est Go 1.27, sans CGO, avec les bibliothèques de l'annexe du CDC | accepté | 2026-09-18 |
| 0003 | Le produit parle anglais, le projet se pilote en français | accepté | 2026-09-18 |
| 0004 | Le périmètre des moteurs est celui du § 3, et la matrice de tests le couvre entièrement | accepté | 2026-09-18 |
| 0005 | Une sauvegarde produit une archive autonome, sans déduplication | accepté | 2026-09-18 |
| 0006 | L'état local est un SQLite unique, aux types explicites | accepté | 2026-09-18 |
| 0007 | L'agent ne détient qu'une clé publique ; la clé privée est fournie à la restauration | accepté | 2026-09-18 |
| 0008 | Deux catégories de sorties : les fonctionnelles et celles d'exploitation | accepté | 2026-09-18 |
| 0009 | L'accès à la machine fait les droits ; le serveur central est le seul acteur distant | accepté | 2026-09-18 |
| 0010 | Le domaine est découpé par cas d'usage, les composants du CDC sont des adaptateurs | accepté | 2026-09-18 |
| 0011 | Windows est hors périmètre, et c'est annoncé | accepté | 2026-09-18 |
| 0012 | Le fichier reçoit tout, la console reçoit ce que son lecteur attend | accepté | 2026-09-18 |
