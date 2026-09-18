# Spike `E-130` — édition de liens des outils gérés

- **Date** : 2026-09-18
- **Exigence** : `E-130` (§ 11, risque n° 1 du CDC). **Conditionne `E-043`** (installation gérée
  des outils) et les scénarios 2 et 3 du § 8.
- **Script** : `scripts/spike-rpath.sh` — rejouable.
- **Conclusion** : **`E-043` est tenable**, y compris sur musl. Voir les réserves en fin de document.

## La question

`E-043` promet que koffr installe lui-même un outil de dump manquant. L'agent n'est pas un
gestionnaire de paquets : il télécharge une archive, la dépose sous `/var/lib/koffr/tools/`, et
exécute le binaire. Rien n'est installé par `apt` ou `dnf`.

Donc l'archive doit porter **toutes** les bibliothèques dont le binaire a besoin, et le binaire doit
être capable de les trouver **sans** `LD_LIBRARY_PATH` — qu'on ne peut pas imposer à un
sous-process lancé par un démon. Le CDC classe ce point en risque n° 1 et demande de le lever avant
`L0`.

## Méthode

Pour chaque outil, un « bundle » est fabriqué depuis une image officielle **glibc**, puis exécuté
dans trois distributions cibles qui n'ont **ni l'outil, ni son serveur, ni ses bibliothèques**.

1. extraire le binaire de l'image source ;
2. copier toute la fermeture `ldd`, **plus le chargeur dynamique** (`ld-linux-*.so`) ;
3. `patchelf --set-interpreter` vers le chargeur du bundle, et un `runpath` sur le binaire
   (`$ORIGIN/../lib`) **et sur chaque bibliothèque du bundle** (`$ORIGIN`) ;
4. lancer `--version` dans chaque cible ;
5. puis — au-delà de ce que le plan demandait — **sauvegarder réellement une base** par TCP, en
   désignant le serveur **par son nom**.

| | |
| --- | --- |
| Outils | `pg_dump` 16.15 (`postgres:16`), `mariadb-dump` 11.4.13 (`mariadb:11.4`) |
| Cibles | `debian:12` (glibc), `rockylinux/rockylinux:9` (glibc 2.34), `alpine:3.20` (**musl**) |
| Architectures | `linux/arm64` natif, `linux/amd64` émulé (poste `darwin/arm64`) |
| glibc embarquée | **2.41** (l'image `postgres:16` est construite sur Debian 13) |

## Résultats — 18 exécutions, 18 succès

| Arch | Outil | Debian 12 | Rocky 9 | Alpine 3.20 |
| --- | --- | --- | --- | --- |
| `arm64` | `pg_dump --version` | ✅ | ✅ | ✅ |
| `arm64` | `mariadb-dump --version` | ✅ | ✅ | ✅ |
| `arm64` | `pg_dump` **dump réel par TCP, par nom** | ✅ | ✅ | ✅ |
| `amd64` | `pg_dump --version` | ✅ | ✅ | ✅ |
| `amd64` | `mariadb-dump --version` | ✅ | ✅ | ✅ |
| `amd64` | `pg_dump` **dump réel par TCP, par nom** | ✅ | ✅ | ✅ |

## Ce qui a cassé, et pourquoi — le vrai enseignement

La **première** version du spike a échoué partout, Debian comprise, avec trois messages différents
selon la distribution : `libssl.so.3` introuvable sur Debian, `libxxhash.so.0` sur Rocky,
`libgssapi_krb5.so.2` sur Alpine. Trois symptômes, une seule cause.

Le bundle était **complet** — les vingt bibliothèques y étaient, `libssl.so.3` incluse. Mais
`patchelf --set-rpath` écrit **`DT_RUNPATH`**, et `DT_RUNPATH` ne vaut **que pour l'objet qui le
déclare** : il n'est **pas hérité** par ses dépendances. Donc `pg_dump` trouvait ses dépendances
directes dans le bundle, mais `libpq.so.5` cherchait **les siennes** dans les chemins du système.
Chaque distribution signalait simplement la première qui lui manquait — d'où l'illusion de trois
problèmes distincts.

**Correction** : poser un `runpath` sur **chaque bibliothèque du bundle**, pas seulement sur le
binaire. Le chargeur (`ld-linux-*.so`) est laissé intact : le modifier le casse.

> À retenir pour `E-043` : un bundle correct ne se vérifie pas en regardant s'il contient les
> bonnes bibliothèques, mais en l'exécutant sur une machine qui n'a rien.

## Pourquoi Alpine passe, contre l'attente du plan

Le plan du lot 0 tenait l'échec sur Alpine pour « le résultat attendu ». Il ne s'est pas produit,
et la raison est précise :

- le bundle embarque **son propre chargeur glibc**, donc musl n'intervient jamais — le binaire
  n'est pas « lancé par Alpine », il est lancé par la glibc qu'on transporte ;
- la résolution de noms, elle, aurait dû casser : glibc passe par NSS, dont les modules sont
  chargés par `dlopen` **hors de la fermeture `ldd`**, et une machine musl n'a ni ces modules ni
  `/etc/nsswitch.conf`. Elle ne casse pas parce que, **depuis glibc 2.34, `nss_files` et `nss_dns`
  sont intégrés à `libc.so.6`**. Vérifié sur le bundle : `_nss_files_gethostbyname4_r` et
  `_nss_dns_gethostbyname4_r` sont des symboles internes de la `libc` embarquée (2.41), et aucune
  `libnss_*.so` séparée n'est présente.

## Ce que le spike impose au produit

1. **Le chemin d'installation d'un outil géré est figé à la fabrication.** `PT_INTERP` est résolu
   par le noyau, qui n'interprète pas `$ORIGIN` : le chemin du chargeur est **absolu**. Un bundle
   patché pour `/var/lib/koffr/tools/...` ne fonctionne **pas** ailleurs. Conséquence : `E-026`
   n'est pas une convention d'installation, c'est une **contrainte d'exécution** ; si `--state-dir`
   déplace l'arborescence, les outils gérés doivent être re-patchés, pas déplacés.
2. **Chaque bibliothèque du bundle est patchée**, pas seulement le binaire.
3. **Le chargeur n'est jamais patché.**
4. Une glibc **récente** (2.41) s'exécute sans peine sur un hôte à glibc **ancienne** (Rocky 9,
   2.34) : c'est le sens qui compte, et il est bon. L'inverse ne se pose pas.

## Ce que le spike ne prouve pas

- **`pg_restore` et les autres binaires** n'ont pas été testés ; ils partagent `libpq` et devraient
  suivre, mais ce n'est pas mesuré.
- **NSS au-delà de `files` et `dns`** : un hôte qui résout ses bases par LDAP ou SSSD utiliserait
  un module NSS externe que le bundle n'a pas. Cas de bord, à documenter, pas à supporter.
- **La provenance des binaires** : ils viennent ici d'images Docker officielles, ce qui n'est pas
  la chaîne de fabrication du produit. `D-06` (qui construit, signe et héberge les archives
  d'outils) et `Q-15` (signature) restent entières et **bloquent toujours une vague du lot 1**.
- **`amd64` est émulé** sur un poste `darwin/arm64`. L'émulation reproduit fidèlement le chargement
  ELF, et le résultat est identique à `arm64` ; à reconfirmer sur une vraie machine `amd64` à la
  première occasion.
- **Aucune base volumineuse** : c'est un test d'édition de liens, pas de performance.

## Conclusion

**`E-043` est tenable et n'a pas à être amendée.** Un outil de dump peut être livré avec ses
bibliothèques et exécuté sur Debian, Rocky **et Alpine**, en `amd64` comme en `arm64`, jusqu'à la
sauvegarde réelle d'une base par TCP. Le lot 1 garde sa vague « installation gérée », qui reste
bloquée par `D-06` et `Q-15`, non par la technique.

L'ADR prévu par la tâche `2.3` du plan — amender `E-043` pour la limiter à glibc — **n'a pas lieu
d'être écrit**.

## Rejouer

```sh
# macOS : l'assistant d'identifiants de Docker Desktop n'est pas dans le PATH par défaut
PATH="/Applications/Docker.app/Contents/Resources/bin:$PATH" ./scripts/spike-rpath.sh all
```

Sortie détaillée dans `.spike/results.tsv` (non versionné). Compte environ vingt minutes en
`arm64`, davantage en `amd64` émulé.
