/*
 Navicat Premium Dump SQL

 Source Server         : Localhost
 Source Server Type    : MySQL
 Source Server Version : 80030 (8.0.30)
 Source Host           : localhost:3306
 Source Schema         : mirofish

 Target Server Type    : MySQL
 Target Server Version : 80030 (8.0.30)
 File Encoding         : 65001

 Date: 07/11/2026 13:00:24
*/

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- ----------------------------
-- Table structure for batch_items
-- ----------------------------
DROP TABLE IF EXISTS `batch_items`;
CREATE TABLE `batch_items`  (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `batch_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `episode_uuid` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  `sequence_index` int NULL DEFAULT NULL,
  `status` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT 'succeeded',
  `error` text CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `graph_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  `data` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `chunk_index` int NULL DEFAULT NULL,
  `chunk_sha256` char(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  PRIMARY KEY (`id`) USING BTREE,
  INDEX `idx_batch_items_batch`(`batch_id` ASC) USING BTREE,
  CONSTRAINT `fk_items_batch` FOREIGN KEY (`batch_id`) REFERENCES `batches` (`batch_id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE = InnoDB AUTO_INCREMENT = 42 CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for batches
-- ----------------------------
DROP TABLE IF EXISTS `batches`;
CREATE TABLE `batches`  (
  `batch_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `graph_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  `batch_metadata` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `status` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT 'draft',
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  PRIMARY KEY (`batch_id`) USING BTREE,
  INDEX `idx_batches_graph`(`graph_id` ASC) USING BTREE,
  INDEX `idx_batches_created`(`created_at` ASC) USING BTREE,
  CONSTRAINT `fk_batches_graph` FOREIGN KEY (`graph_id`) REFERENCES `graphs` (`graph_id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE = InnoDB CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for edges
-- ----------------------------
DROP TABLE IF EXISTS `edges`;
CREATE TABLE `edges`  (
  `uuid` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `graph_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `name` text CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `fact` text CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `source_node_uuid` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  `target_node_uuid` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  `attributes` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  `valid_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  `invalid_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  `expired_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  PRIMARY KEY (`uuid`) USING BTREE,
  INDEX `idx_edges_graph`(`graph_id` ASC) USING BTREE,
  INDEX `idx_edges_source`(`source_node_uuid` ASC) USING BTREE,
  INDEX `idx_edges_target`(`target_node_uuid` ASC) USING BTREE,
  FULLTEXT INDEX `ft_edges`(`fact`, `name`),
  CONSTRAINT `fk_edges_graph` FOREIGN KEY (`graph_id`) REFERENCES `graphs` (`graph_id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE = InnoDB CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for episodes
-- ----------------------------
DROP TABLE IF EXISTS `episodes`;
CREATE TABLE `episodes`  (
  `uuid` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `graph_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `data` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `data_type` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  `source_description` text CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `episode_metadata` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `processed` tinyint NOT NULL DEFAULT 1,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  PRIMARY KEY (`uuid`) USING BTREE,
  INDEX `idx_episodes_graph`(`graph_id` ASC) USING BTREE,
  INDEX `idx_episodes_processed`(`processed` ASC) USING BTREE,
  CONSTRAINT `fk_episodes_graph` FOREIGN KEY (`graph_id`) REFERENCES `graphs` (`graph_id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE = InnoDB CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for graphs
-- ----------------------------
DROP TABLE IF EXISTS `graphs`;
CREATE TABLE `graphs`  (
  `graph_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `name` text CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `description` text CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `ontology` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  PRIMARY KEY (`graph_id`) USING BTREE
) ENGINE = InnoDB CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for nodes
-- ----------------------------
DROP TABLE IF EXISTS `nodes`;
CREATE TABLE `nodes`  (
  `uuid` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `graph_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `name` varchar(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `labels` text CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `summary` text CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `attributes` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  PRIMARY KEY (`uuid`) USING BTREE,
  UNIQUE INDEX `idx_nodes_graph_name`(`graph_id` ASC, `name` ASC) USING BTREE,
  INDEX `idx_nodes_graph`(`graph_id` ASC) USING BTREE,
  FULLTEXT INDEX `ft_nodes`(`name`, `summary`),
  CONSTRAINT `fk_nodes_graph` FOREIGN KEY (`graph_id`) REFERENCES `graphs` (`graph_id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE = InnoDB CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for oasis_actions
-- ----------------------------
DROP TABLE IF EXISTS `oasis_actions`;
CREATE TABLE `oasis_actions`  (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `simulation_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `round_num` int NOT NULL DEFAULT 0,
  `agent_id` int NOT NULL,
  `agent_name` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL,
  `action_type` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `action_args` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `result` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `success` tinyint NOT NULL DEFAULT 1,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  PRIMARY KEY (`id`) USING BTREE,
  INDEX `idx_oasis_actions_sim`(`simulation_id` ASC, `id` ASC) USING BTREE,
  INDEX `idx_oasis_actions_round`(`simulation_id` ASC, `round_num` ASC) USING BTREE,
  INDEX `idx_oasis_actions_agent`(`simulation_id` ASC, `agent_id` ASC) USING BTREE
) ENGINE = InnoDB AUTO_INCREMENT = 306 CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for oasis_comments
-- ----------------------------
DROP TABLE IF EXISTS `oasis_comments`;
CREATE TABLE `oasis_comments`  (
  `comment_id` bigint NOT NULL AUTO_INCREMENT,
  `simulation_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `post_id` bigint NOT NULL,
  `user_id` bigint NOT NULL,
  `content` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  `num_likes` int NOT NULL DEFAULT 0,
  `num_dislikes` int NOT NULL DEFAULT 0,
  PRIMARY KEY (`comment_id`) USING BTREE,
  INDEX `idx_oasis_comments_sim`(`simulation_id` ASC, `comment_id` ASC) USING BTREE,
  INDEX `idx_oasis_comments_post`(`post_id` ASC, `created_at` ASC) USING BTREE,
  INDEX `fk_oasis_comments_user`(`user_id` ASC) USING BTREE,
  CONSTRAINT `fk_oasis_comments_post` FOREIGN KEY (`post_id`) REFERENCES `oasis_posts` (`post_id`) ON DELETE CASCADE ON UPDATE RESTRICT,
  CONSTRAINT `fk_oasis_comments_user` FOREIGN KEY (`user_id`) REFERENCES `oasis_users` (`id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE = InnoDB AUTO_INCREMENT = 88 CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for oasis_follows
-- ----------------------------
DROP TABLE IF EXISTS `oasis_follows`;
CREATE TABLE `oasis_follows`  (
  `follow_id` bigint NOT NULL AUTO_INCREMENT,
  `simulation_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `follower_id` bigint NOT NULL,
  `followee_id` bigint NOT NULL,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  PRIMARY KEY (`follow_id`) USING BTREE,
  UNIQUE INDEX `uq_oasis_follow`(`simulation_id` ASC, `follower_id` ASC, `followee_id` ASC) USING BTREE,
  INDEX `idx_oasis_follows_followee`(`followee_id` ASC) USING BTREE,
  INDEX `fk_oasis_follows_follower`(`follower_id` ASC) USING BTREE,
  CONSTRAINT `fk_oasis_follows_followee` FOREIGN KEY (`followee_id`) REFERENCES `oasis_users` (`id`) ON DELETE CASCADE ON UPDATE RESTRICT,
  CONSTRAINT `fk_oasis_follows_follower` FOREIGN KEY (`follower_id`) REFERENCES `oasis_users` (`id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE = InnoDB AUTO_INCREMENT = 1 CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for oasis_likes
-- ----------------------------
DROP TABLE IF EXISTS `oasis_likes`;
CREATE TABLE `oasis_likes`  (
  `like_id` bigint NOT NULL AUTO_INCREMENT,
  `simulation_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `user_id` bigint NOT NULL,
  `post_id` bigint NOT NULL,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  PRIMARY KEY (`like_id`) USING BTREE,
  UNIQUE INDEX `uq_oasis_like`(`simulation_id` ASC, `user_id` ASC, `post_id` ASC) USING BTREE,
  INDEX `idx_oasis_likes_post`(`post_id` ASC) USING BTREE,
  INDEX `fk_oasis_likes_user`(`user_id` ASC) USING BTREE,
  CONSTRAINT `fk_oasis_likes_post` FOREIGN KEY (`post_id`) REFERENCES `oasis_posts` (`post_id`) ON DELETE CASCADE ON UPDATE RESTRICT,
  CONSTRAINT `fk_oasis_likes_user` FOREIGN KEY (`user_id`) REFERENCES `oasis_users` (`id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE = InnoDB AUTO_INCREMENT = 1 CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for oasis_posts
-- ----------------------------
DROP TABLE IF EXISTS `oasis_posts`;
CREATE TABLE `oasis_posts`  (
  `post_id` bigint NOT NULL AUTO_INCREMENT,
  `simulation_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `user_id` bigint NOT NULL,
  `original_post_id` bigint NULL DEFAULT NULL,
  `content` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `quote_content` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  `num_likes` int NOT NULL DEFAULT 0,
  `num_dislikes` int NOT NULL DEFAULT 0,
  `num_shares` int NOT NULL DEFAULT 0,
  `num_reports` int NOT NULL DEFAULT 0,
  PRIMARY KEY (`post_id`) USING BTREE,
  INDEX `idx_oasis_posts_sim`(`simulation_id` ASC, `post_id` ASC) USING BTREE,
  INDEX `idx_oasis_posts_user`(`user_id` ASC) USING BTREE,
  INDEX `fk_oasis_posts_original`(`original_post_id` ASC) USING BTREE,
  CONSTRAINT `fk_oasis_posts_original` FOREIGN KEY (`original_post_id`) REFERENCES `oasis_posts` (`post_id`) ON DELETE SET NULL ON UPDATE RESTRICT,
  CONSTRAINT `fk_oasis_posts_user` FOREIGN KEY (`user_id`) REFERENCES `oasis_users` (`id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE = InnoDB AUTO_INCREMENT = 213 CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for oasis_rec
-- ----------------------------
DROP TABLE IF EXISTS `oasis_rec`;
CREATE TABLE `oasis_rec`  (
  `simulation_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `user_id` bigint NOT NULL,
  `post_id` bigint NOT NULL,
  PRIMARY KEY (`simulation_id`, `user_id`, `post_id`) USING BTREE,
  INDEX `idx_oasis_rec_post`(`post_id` ASC) USING BTREE,
  INDEX `fk_oasis_rec_user`(`user_id` ASC) USING BTREE,
  CONSTRAINT `fk_oasis_rec_post` FOREIGN KEY (`post_id`) REFERENCES `oasis_posts` (`post_id`) ON DELETE CASCADE ON UPDATE RESTRICT,
  CONSTRAINT `fk_oasis_rec_user` FOREIGN KEY (`user_id`) REFERENCES `oasis_users` (`id`) ON DELETE CASCADE ON UPDATE RESTRICT
) ENGINE = InnoDB CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for oasis_runs
-- ----------------------------
DROP TABLE IF EXISTS `oasis_runs`;
CREATE TABLE `oasis_runs`  (
  `simulation_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `state_json` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `updated_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  PRIMARY KEY (`simulation_id`) USING BTREE
) ENGINE = InnoDB CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for oasis_traces
-- ----------------------------
DROP TABLE IF EXISTS `oasis_traces`;
CREATE TABLE `oasis_traces`  (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `simulation_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `user_id` bigint NOT NULL,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  `action` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `info` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  PRIMARY KEY (`id`) USING BTREE,
  INDEX `idx_oasis_traces_sim`(`simulation_id` ASC, `id` ASC) USING BTREE,
  INDEX `idx_oasis_traces_user`(`simulation_id` ASC, `user_id` ASC, `action` ASC) USING BTREE
) ENGINE = InnoDB AUTO_INCREMENT = 689 CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

-- ----------------------------
-- Table structure for oasis_users
-- ----------------------------
DROP TABLE IF EXISTS `oasis_users`;
CREATE TABLE `oasis_users`  (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `simulation_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `agent_id` int NOT NULL,
  `user_name` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `name` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `bio` text CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `persona` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  `created_at` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  `num_followings` int NOT NULL DEFAULT 0,
  `num_followers` int NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`) USING BTREE,
  UNIQUE INDEX `uq_oasis_user`(`simulation_id` ASC, `agent_id` ASC) USING BTREE,
  INDEX `idx_oasis_users_sim`(`simulation_id` ASC) USING BTREE
) ENGINE = InnoDB AUTO_INCREMENT = 76 CHARACTER SET = utf8mb4 COLLATE = utf8mb4_bin ROW_FORMAT = Dynamic;

SET FOREIGN_KEY_CHECKS = 1;
