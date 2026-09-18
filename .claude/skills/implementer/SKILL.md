---
name: implementer
description: Ajouter ou faire évoluer un module, un cas d'usage, un écran ou un job de {{PROJET}} en respectant l'architecture, le TDD et la Definition of done — structure d'un module, exemples canoniques de la stack, pièges connus. Gabarit rempli au lot 0 pour la stack retenue, amendé à chaque rétro ; à utiliser pour toute tâche de code.
---

# Implémenter — {{stack}}

> **Gabarit.** Rempli au lot 0 depuis l'ADR de stack et le module de bout en bout ; amendé par
> `/cloturer-lot`. Ce skill est **thématique et au présent** : pas de « leçons du lot N », pas de
> numéro de version, pas d'historique. Une leçon se fond dans la section où le lecteur la
> cherchera. Les références de vérité restent `CLAUDE.md`, `ARCHITECTURE.md` et `docs/adr/` ; ce
> skill montre comment les appliquer. S'il dépasse 250 lignes, on le scinde par thème.

## Avant d'écrire

1. Lire `ROADMAP.md` (lot en cours), le plan, et le `rules.md` du module visé.
2. Lire l'exigence `E-nn` **et son § du CDC**. Ce qu'il ne dit pas se demande (`docs/questions.md`),
   ne s'invente pas.
3. **Mesurer avant de modéliser** si une donnée existe : comptages, valeurs distinctes, cas limites.
4. Écrire la règle `MOD-nn` dans `rules.md` **avant** le code : énoncé, source, test.
5. **TDD**, sans exception pour le domaine et le serveur : voir la section suivante.

## TDD : le cycle, tel qu'on le tient ici

`CLAUDE.md` fixe la règle ; voici le geste. Une tâche de code se déroule **dans cet ordre**, et on
le montre dans la conversation (la commande lancée et son résultat, pas « les tests passent »).

1. **Nommer le test depuis la règle.** Le fichier de test vit à côté du code (`service.spec.ts`
   pour `service.ts`) ; chaque cas porte le numéro de la règle : `it('MOD-07 refuse une remise
   supérieure à 100 %')`. Un test sans numéro de règle est un test dont on ne sait pas ce qu'il
   prouve.
2. **Écrire le test d'abord**, avec ses trois cas quand ils existent : nominal, limite, erreur
   typée. Sur une base réelle si le module écrit (fixture minimale, transaction annulée à la fin ;
   jamais de mock de la base).
3. **Lancer et constater le rouge** : `{{pnpm test:unit -- --run <fichier>}}`. Le rouge doit
   échouer **pour la bonne raison** (l'assertion, pas un import manquant). Coller la ligne d'échec.
4. **Écrire le code minimal** qui fait passer le test. Pas d'anticipation d'un cas qui n'a pas
   encore son test.
5. **Vert**, même commande. Puis refactor si besoin, le test restant vert.
6. **Élargir** : le cas suivant de la règle, ou la règle suivante. Un cas limite qu'on découvre en
   codant s'écrit d'abord comme test, puis se code.

Ce qui n'est pas du TDD et qu'on ne fait pas : écrire le code puis « ajouter les tests » ; écrire
dix tests puis tout le code ; adapter le test au code quand il échoue ; passer un test en `skip`
pour merger.

Ce qu'on teste autrement, et qu'on **dit** dans la tâche du plan : un composant d'interface (test
de composant : rendu, interaction, états vide / erreur / chargement), un parcours (test de bout en
bout sur les parcours critiques), une configuration ou un script unique (vérification manuelle
consignée dans le plan).

## Structure d'un module

```
{{À remplir : l'arbre d'un module avec le rôle d'un fichier par ligne, tel qu'il existe dans le
module de bout en bout du lot 0.}}
```

## Exemples canoniques

Chaque exemple est **court, réel et cité** (chemin du fichier vivant qui le montre). On n'invente
pas d'exemple : on pointe le code qui fait foi.

### Cas d'usage : droits, transaction, verrou optimiste

{{Extrait de 20 lignes du service du module de référence, avec le chemin.}}

### Exposition : une déclaration, tous les canaux

{{Comment un cas d'usage devient une route, une API, un outil.}}

### Adaptateur : une route fine

{{Validation de forme, construction du contexte, appel du service, traduction des erreurs.}}

### Composant d'interface

{{Le composant de référence, ses conventions d'état et d'événements.}}

### Test de domaine

{{Le test de référence : fixture, cas nominal, cas d'erreur typée, sur base réelle si le module écrit.}}

### Traitement différé

{{Un job de référence : idempotence, clé de déduplication, reprise, aucune exception avalée.}}

### Appel sortant

{{Un adaptateur derrière la porte de sortie : jamais de client réseau ailleurs.}}

## Écrans

### Ce qu'un écran doit à l'utilisateur

- Les champs, colonnes et libellés du CDC ; rien d'inventé, rien d'omis sans décision.
- Pas de phrase d'explication : des champs, des valeurs, des libellés courts, un message au moment
  de l'action.
- Un état de chargement, un état vide, un état d'erreur, un conflit de version.
- Clavier : {{raccourcis convenus}}.

### Tests d'écran

{{Ce qu'on teste en composant, ce qu'on teste en parcours.}}

## Données

{{Ce que les ADR fixent, appliqué : type des montants, des dates, des statuts, colonnes d'audit,
verrou optimiste, agrégats dans la base.}}

## Pièges de la stack

{{Une puce par piège **rencontré** : le symptôme, la cause, le geste. Alimenté par les rétros.
Un piège que le lint attrape n'a pas besoin d'être ici.}}

## Definition of done

Celle de `METHODE.md`. Avant de dire « terminé » : `verify` vert, règle dans `rules.md` avec test,
frontières tenues, droits vérifiés dans le service, migration relue, anglais dans le code, français
dans le pilotage, plan coché.
