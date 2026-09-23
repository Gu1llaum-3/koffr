# ADR-0017 — La structure d'un dump se vérifie au vol, jamais sur l'archive écrite

- **Date** : 2026-09-23
- **Statut** : **accepté** — 2026-09-23
- **Exigences** : `E-008`, `E-014`, `E-062`, `E-063`, `E-113`
- **Références** : CDC § 5.4 `F4.2`, § 2 `P4`, § 6 `F6.3` ; **amende la lecture littérale de
  `F4.2`** ; s'appuie sur ADR-0007 (koffr ne détient qu'une clé publique) et ADR-0016

## Contexte

Le cahier des charges demande deux choses qui ne peuvent pas être vraies en même temps.

`F4.2` veut une vérification structurelle **de l'archive** : « pour PostgreSQL, `pg_restore --list`
sur l'archive doit produire une table des matières cohérente ». `F6.3`, `E-113` et ADR-0007 veulent
qu'un agent compromis n'obtienne **pas** le déchiffrement des archives passées : koffr ne détient
qu'une clé publique, et la clé privée n'est nulle part sur la machine.

Or une archive koffr est chiffrée. **`pg_restore` ne peut pas la lire.** Aucune `Q-nn` ne couvrait
ce point ; il est apparu en écrivant le plan du lot 3.

Deux mesures ont été prises sur l'instance de recette le 2026-09-22, sur une base PostgreSQL 16 de
372 Mo :

| Constat | Résultat |
| --- | --- |
| `pg_dump -Fc \| pg_restore --list` depuis un **tube**, sans fichier | fonctionne, code 0, 24 entrées de table des matières |
| le même sur un flux **tronqué à 200 Ko** | **code 0 également** |
| marqueur de fin d'un dump MariaDB | `-- Dump completed on 2026-09-20 14:12:28` |

La seconde mesure est la plus instructive : **`pg_restore --list` valide la table des matières, pas
l'archive entière.** Il lit la tête du flux et s'arrête. C'est exactement ce que `F4.2` demande —
« une table des matières cohérente » — et cela veut dire que l'empreinte et la structure attrapent
**deux défauts différents** : la structure voit un flux qui n'est pas un dump, l'empreinte voit une
archive tronquée ou corrompue. Aucune ne remplace l'autre, et `P4` exige les deux.

## Décision

**La vérification structurelle se fait au vol, pendant le dump, sur le flux en clair — le seul
instant où koffr le voit. L'empreinte, elle, se recalcule en relisant l'archive écrite.**

- **Structure** : `pg_restore --list` sur la **tête** du flux pour PostgreSQL ; le marqueur de fin
  de dump sur la **queue** pour MySQL et MariaDB. Sur une **fenêtre bornée**, pas une seconde passe
  complète : `E-025` interdit de matérialiser le dump, et une seconde lecture intégrale doublerait
  la charge sur la production.
- **Empreinte** : recalculée en relisant la destination (`E-062`), **sans aucune clé** — une archive
  chiffrée se hache très bien.
- **`koffr verify <backup-id>`** rejoue l'empreinte, met le catalogue à jour, et **dit** qu'il ne
  peut pas rejouer la structure sans la clé privée. Il ne la demande pas et ne l'accepte pas.
- **Le manifeste est écrit après la vérification**, comme `E-024` l'ordonne — d'où le fait qu'il
  puisse porter `verified`.
- La divergence avec la lettre de `F4.2` est **écrite** dans `internal/domain/verify/rules.md`, avec
  ce renvoi.

## Conséquences

- **`P4` devient tenable.** Si la structure exigeait la clé privée, **aucune** sauvegarde
  automatique ne serait jamais « vérifiée » : la rétention du lot 5, qui ne garde que les archives
  vérifiées, n'aurait rien à garder, et `F5.3` — un job réussi suppose qu'une destination a « reçu
  **et vérifié** » — serait inatteignable sans intervention humaine chaque nuit.
- **La structure est vérifiée une fois, à l'écriture, et jamais rejouée.** Une corruption survenue
  **après** coup est vue par l'empreinte, pas par la structure. C'est une perte réelle par rapport à
  la lecture littérale de `F4.2`, et elle est le prix d'ADR-0007.
- **`E-065`** — la revérification périodique du lot 6 — ne pourra porter que sur l'empreinte, pour
  la même raison. À dire quand ce lot sera planifié.
- **La chaîne du lot 2 gagne un consommateur.** `internal/pipeline` doit tolérer que le flux soit
  observé sans être consommé, et que l'observateur parte avant la fin.
- **Ce qui rouvrirait la décision** : un format d'archive qui exposerait une table des matières
  **en clair** à côté du contenu chiffré ; ou un exploitant qui accepterait qu'une clé privée de
  vérification vive sur la machine — ce qu'ADR-0007 a écarté, et qui demanderait de le remplacer.

## Alternatives écartées

- **`koffr verify` demande la clé privée à l'exploitant.** Fidèle à la lettre de `F4.2` : on relit
  vraiment l'archive écrite. Écartée parce qu'aucune sauvegarde automatique ne serait alors vérifiée
  au sens de `P4`, et parce que manipuler une clé privée sur l'agent est exactement ce qu'ADR-0007 a
  voulu éviter.
- **Les deux chemins** — au vol pendant le job, et une commande manuelle avec clé. Écartée pour le
  coût : deux chemins de vérification à écrire et à tenir, dont un qui fait entrer une clé privée
  sur la machine pour un gain que la mesure ne justifie pas.
- **Mettre le tampon en clair pour pouvoir le relire.** Écartée : le § 4.5 met en tampon le flux
  **déjà chiffré**, précisément pour qu'un disque volé ne livre rien.
- **Se contenter de l'empreinte et abandonner `F4.2`.** Écartée : l'empreinte ne distingue pas une
  archive intacte d'un dump qui n'en était pas un — un `pg_dump` qui échoue en écrivant deux octets
  produit une archive parfaitement cohérente de rien.
