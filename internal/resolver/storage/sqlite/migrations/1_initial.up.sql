CREATE TABLE bots (
    ttm_account     VARBINARY(20)  NOT NULL,
    bot             VARBINARY(20)  NOT NULL,
    status          INTEGER        NOT NULL,
    PRIMARY KEY (ttm_account, bot)
);
