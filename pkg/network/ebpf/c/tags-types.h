#ifndef __TAGS_TYPES_H
#define __TAGS_TYPES_H

// static tags limited to 64 tags per unique connection
enum static_tags {
    HTTP = (1<<0),
    LIBGNUTLS = (1<<1),
    LIBSSL = (1<<2),
    TLS = (1<<3),
};

#endif
